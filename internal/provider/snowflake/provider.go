/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package snowflake

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/nikhil-thomas/Resource-Pool/internal/provider"
)

const (
	// ConfigMapName is the name of the ConfigMap containing Snowflake account configuration
	ConfigMapName = "snowflake-config"
	// ConfigMapKey is the key in the ConfigMap data that contains the YAML
	ConfigMapKey = "config.yaml"
)

// SnowflakeProvider implements the ResourceProvider interface for Snowflake
type SnowflakeProvider struct {
	client    client.Client
	reader    client.Reader
	config    *SnowflakeConfig
	namespace string
}

// New creates a new Snowflake provider.
// reader is used during Initialize (before the cache is started) and may be mgr.GetAPIReader().
// c is used for runtime operations (reads and writes after the cache is started).
func New(c client.Client, reader client.Reader) *SnowflakeProvider {
	return &SnowflakeProvider{
		client: c,
		reader: reader,
	}
}

// Name returns the provider type name
func (p *SnowflakeProvider) Name() string {
	return "snowflake"
}

// Initialize loads configuration and returns list of managed resources
func (p *SnowflakeProvider) Initialize(ctx context.Context, namespace string) ([]provider.Resource, error) {
	log := log.FromContext(ctx)
	p.namespace = namespace

	// Load configuration from ConfigMap using the direct API reader (bypasses cache).
	cm := &corev1.ConfigMap{}
	err := p.reader.Get(ctx, client.ObjectKey{
		Name:      ConfigMapName,
		Namespace: namespace,
	}, cm)
	if err != nil {
		return nil, fmt.Errorf("failed to get configmap %s/%s: %w", namespace, ConfigMapName, err)
	}

	yamlData, exists := cm.Data[ConfigMapKey]
	if !exists {
		return nil, fmt.Errorf("configmap %s/%s missing key %s", namespace, ConfigMapName, ConfigMapKey)
	}

	// Parse YAML configuration
	config := &SnowflakeConfig{}
	if err := yaml.Unmarshal([]byte(yamlData), config); err != nil {
		return nil, fmt.Errorf("failed to parse snowflake config: %w", err)
	}

	// Validate configuration
	if len(config.Accounts) == 0 {
		return nil, fmt.Errorf("no accounts defined in configuration")
	}

	for i, account := range config.Accounts {
		if account.Name == "" {
			return nil, fmt.Errorf("account at index %d missing name", i)
		}
		if account.AccountURL == "" {
			return nil, fmt.Errorf("account %s missing accountUrl", account.Name)
		}
		if account.CredentialSecretName == "" {
			return nil, fmt.Errorf("account %s missing credentialSecretName", account.Name)
		}
	}

	p.config = config

	// Convert to provider.Resource
	resources := make([]provider.Resource, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		resources = append(resources, provider.Resource{
			Name:                 account.Name,
			Labels:               account.Labels,
			CredentialSecretName: account.CredentialSecretName,
			Metadata: map[string]string{
				"accountUrl": account.AccountURL,
				"warehouse":  account.Warehouse,
				"database":   account.Database,
				"schema":     account.Schema,
				"role":       account.Role,
			},
		})
	}

	log.Info("Snowflake provider initialized", "accountCount", len(resources))
	return resources, nil
}

// GetConnectionDetails returns connection information for a resource
func (p *SnowflakeProvider) GetConnectionDetails(ctx context.Context, resource provider.Resource) (map[string]string, error) {
	// Find the account config
	var account *SnowflakeAccount
	for i := range p.config.Accounts {
		if p.config.Accounts[i].Name == resource.Name {
			account = &p.config.Accounts[i]
			break
		}
	}

	if account == nil {
		return nil, fmt.Errorf("account %s not found in configuration", resource.Name)
	}

	// Extract account identifier from URL
	accountIdentifier := extractAccountIdentifier(account.AccountURL)

	return map[string]string{
		"accountUrl":        account.AccountURL,
		"warehouse":         account.Warehouse,
		"database":          account.Database,
		"schema":            account.Schema,
		"role":              account.Role,
		"accountIdentifier": accountIdentifier,
	}, nil
}

// GetCredentialSecret retrieves the Secret for a resource
func (p *SnowflakeProvider) GetCredentialSecret(ctx context.Context, namespace string, resource provider.Resource) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := p.client.Get(ctx, client.ObjectKey{
		Name:      resource.CredentialSecretName,
		Namespace: namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, resource.CredentialSecretName, err)
	}
	return secret, nil
}

// AcquireResource is called when a lease is acquired (optional setup)
func (p *SnowflakeProvider) AcquireResource(ctx context.Context, resource provider.Resource, claimName string) error {
	log := log.FromContext(ctx)
	log.Info("Acquired Snowflake resource", "resource", resource.Name, "claim", claimName)
	// No special setup needed for Snowflake
	return nil
}

// ReleaseResource resets state and rotates credentials
func (p *SnowflakeProvider) ReleaseResource(ctx context.Context, namespace string, resource provider.Resource) error {
	log := log.FromContext(ctx)
	log.Info("Releasing Snowflake resource", "resource", resource.Name)

	// Find the account config to get reset queries
	var account *SnowflakeAccount
	for i := range p.config.Accounts {
		if p.config.Accounts[i].Name == resource.Name {
			account = &p.config.Accounts[i]
			break
		}
	}

	if account == nil {
		return fmt.Errorf("account %s not found in configuration", resource.Name)
	}

	// Get credentials from secret
	secret, err := p.GetCredentialSecret(ctx, namespace, resource)
	if err != nil {
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	username := string(secret.Data["username"])
	privateKey := secret.Data["privateKey"]

	if username == "" || len(privateKey) == 0 {
		return fmt.Errorf("secret %s missing username or privateKey", resource.CredentialSecretName)
	}

	// Connect to Snowflake
	sfClient, err := NewClient(ctx, ConnectionParams{
		AccountURL: account.AccountURL,
		Username:   username,
		Role:       account.Role,
		Warehouse:  account.Warehouse,
		Database:   account.Database,
		Schema:     account.Schema,
		PrivateKey: privateKey,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Snowflake: %w", err)
	}
	defer sfClient.Close()

	// Reset state (run custom queries)
	if err := sfClient.ResetState(ctx, account.ResetQueries); err != nil {
		log.Error(err, "Failed to reset Snowflake state", "resource", resource.Name)
		// Continue with credential rotation even if reset fails
	}

	// Generate new key pair
	newPrivateKey, newPublicKey, err := GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Rotate credentials in Snowflake
	if err := sfClient.RotateCredentials(ctx, newPublicKey); err != nil {
		log.Error(err, "Failed to rotate Snowflake credentials", "resource", resource.Name)
		// Don't fail the cleanup, but log the error
	} else {
		// Update secret with new private key
		secret.Data["privateKey"] = newPrivateKey
		if err := p.client.Update(ctx, secret); err != nil {
			log.Error(err, "Failed to update secret with new private key", "secret", resource.CredentialSecretName)
		} else {
			log.Info("Successfully rotated credentials and updated secret", "resource", resource.Name)
		}
	}

	log.Info("Snowflake resource released", "resource", resource.Name)
	return nil
}

// extractAccountIdentifier extracts account ID from URL
// Example: "xy12345.us-east-1.snowflakecomputing.com" -> "xy12345"
func extractAccountIdentifier(accountURL string) string {
	// Remove protocol if present
	accountURL = strings.TrimPrefix(accountURL, "https://")
	accountURL = strings.TrimPrefix(accountURL, "http://")

	// Remove .snowflakecomputing.com suffix if present
	accountURL = strings.TrimSuffix(accountURL, ".snowflakecomputing.com")

	// Extract first part (account ID)
	parts := strings.Split(accountURL, ".")
	if len(parts) > 0 {
		return parts[0]
	}

	return accountURL
}
