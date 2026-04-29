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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
	logger := logf.FromContext(ctx)
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

	logger.Info("Snowflake provider initialized", "accountCount", len(resources))
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

const appUser = "test_runner_appuser"

// AcquireResource creates the app user in Snowflake, generates a fresh key pair,
// stores the private key in a per-claim Secret, and returns the Secret name.
func (p *SnowflakeProvider) AcquireResource(ctx context.Context, resource provider.Resource, claimName string) (string, error) {
	logger := logf.FromContext(ctx)
	logger.Info("Acquiring Snowflake resource", "resource", resource.Name, "claim", claimName)

	// Find account config
	var account *SnowflakeAccount
	for i := range p.config.Accounts {
		if p.config.Accounts[i].Name == resource.Name {
			account = &p.config.Accounts[i]
			break
		}
	}
	if account == nil {
		return "", fmt.Errorf("account %s not found in configuration", resource.Name)
	}

	// Load admin credentials from the pool secret
	adminSecret := &corev1.Secret{}
	// claimName is "namespace/name"
	namespace, _, _ := strings.Cut(claimName, "/")
	if err := p.client.Get(ctx, client.ObjectKey{
		Name:      resource.CredentialSecretName,
		Namespace: namespace,
	}, adminSecret); err != nil {
		return "", fmt.Errorf("failed to get admin secret: %w", err)
	}

	adminUsername := string(adminSecret.Data["username"])
	adminPrivateKey := adminSecret.Data["privateKey"]
	if adminUsername == "" || len(adminPrivateKey) == 0 {
		return "", fmt.Errorf("admin secret %s missing username or privateKey", resource.CredentialSecretName)
	}

	// Connect to Snowflake as admin using the admin user's own default role.
	// No warehouse needed — admin only executes DDL (CREATE/ALTER USER).
	sfClient, err := NewClient(ctx, ConnectionParams{
		AccountURL: account.AccountURL,
		Username:   adminUsername,
		PrivateKey: adminPrivateKey,
	})
	if err != nil {
		return "", fmt.Errorf("failed to connect to Snowflake: %w", err)
	}
	defer func() { _ = sfClient.Close() }()

	// Ensure the app user exists
	if err := sfClient.EnsureUser(ctx, appUser, account.Role, account.Warehouse); err != nil {
		return "", fmt.Errorf("failed to ensure app user: %w", err)
	}

	// Grant configured roles to the app user
	for _, role := range account.AppUserRoles {
		if err := sfClient.GrantRole(ctx, role, appUser); err != nil {
			return "", fmt.Errorf("failed to grant role %s: %w", role, err)
		}
	}

	// Generate a fresh key pair for the app user
	privKeyPEM, pubKey, err := GenerateKeyPair()
	if err != nil {
		return "", fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Set public key slot 1, clear slot 2
	if err := sfClient.SetPublicKey(ctx, appUser, pubKey); err != nil {
		return "", fmt.Errorf("failed to set public key: %w", err)
	}

	// Derive a deterministic secret name from the claim identity
	// claimName = "namespace/name" → "sf-appuser-namespace-name"
	secretName := "sf-appuser-" + strings.ReplaceAll(claimName, "/", "-")

	// Create or update the per-claim Secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"username":   []byte(appUser),
			"privateKey": privKeyPEM,
		},
	}

	existing := &corev1.Secret{}
	err = p.client.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, existing)
	if apierrors.IsNotFound(err) {
		if err := p.client.Create(ctx, secret); err != nil {
			return "", fmt.Errorf("failed to create app user secret: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("failed to check for existing secret: %w", err)
	} else {
		existing.Data = secret.Data
		if err := p.client.Update(ctx, existing); err != nil {
			return "", fmt.Errorf("failed to update app user secret: %w", err)
		}
	}

	logger.Info("App user ready and secret stored", "user", appUser, "secret", secretName)
	return secretName, nil
}

// ReleaseResource cleans up after a claim is deleted:
//  1. Runs reset queries as the app user (best-effort)
//  2. Unsets both RSA key slots for the app user (via admin)
//  3. Deletes the per-claim app user Secret from Kubernetes
//
// The bootstrap admin secret is never modified — it holds long-lived service
// account credentials that must remain stable across all acquire/release cycles.
func (p *SnowflakeProvider) ReleaseResource(ctx context.Context, namespace string, resource provider.Resource, claimName string) error {
	log := logf.FromContext(ctx)
	log.Info("Releasing Snowflake resource", "resource", resource.Name, "claim", claimName)

	// Find account config
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

	// Load admin credentials
	adminSecret, err := p.GetCredentialSecret(ctx, namespace, resource)
	if err != nil {
		return fmt.Errorf("failed to get admin credentials: %w", err)
	}

	adminUsername := string(adminSecret.Data["username"])
	adminPrivateKey := adminSecret.Data["privateKey"]
	if adminUsername == "" || len(adminPrivateKey) == 0 {
		return fmt.Errorf("admin secret %s missing username or privateKey", resource.CredentialSecretName)
	}

	// Connect to Snowflake as admin using the admin user's own default role.
	// No warehouse needed — admin only executes DDL (ALTER USER, credential rotation).
	adminClient, err := NewClient(ctx, ConnectionParams{
		AccountURL: account.AccountURL,
		Username:   adminUsername,
		PrivateKey: adminPrivateKey,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Snowflake as admin: %w", err)
	}
	defer func() { _ = adminClient.Close() }()

	// Step 1: Run reset queries as the app user (best-effort)
	appUserSecretName := "sf-appuser-" + strings.ReplaceAll(claimName, "/", "-")
	appUserSecret := &corev1.Secret{}
	claimNamespace, _, _ := strings.Cut(claimName, "/")
	err = p.client.Get(ctx, client.ObjectKey{Name: appUserSecretName, Namespace: claimNamespace}, appUserSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to look up app user secret, skipping reset queries", "secret", appUserSecretName)
	} else if err == nil {
		appPrivateKey := appUserSecret.Data["privateKey"]
		if len(appPrivateKey) > 0 {
			appClient, appErr := NewClient(ctx, ConnectionParams{
				AccountURL: account.AccountURL,
				Username:   appUser,
				Role:       account.Role,
				Warehouse:  account.Warehouse,
				Database:   account.Database,
				Schema:     account.Schema,
				PrivateKey: appPrivateKey,
			})
			if appErr != nil {
				log.Error(appErr, "Failed to connect as app user, skipping reset queries")
			} else {
				if resetErr := appClient.ResetState(ctx, account.ResetQueries); resetErr != nil {
					log.Error(resetErr, "Failed to reset Snowflake state", "resource", resource.Name)
				}
				if closeErr := appClient.Close(); closeErr != nil {
					log.Error(closeErr, "Failed to close app client")
				}
			}
		}
	}

	// Step 2: Revoke roles from the app user, then unset both RSA key slots (via admin)
	for _, role := range account.AppUserRoles {
		adminClient.RevokeRole(ctx, role, appUser)
	}
	adminClient.ClearPublicKeys(ctx, appUser)

	// Step 3: Delete the per-claim app user Secret
	if appUserSecret.Name != "" {
		if delErr := p.client.Delete(ctx, appUserSecret); delErr != nil && !apierrors.IsNotFound(delErr) {
			log.Error(delErr, "Failed to delete app user secret", "secret", appUserSecretName)
		} else {
			log.Info("Deleted app user secret", "secret", appUserSecretName)
		}
	}

	log.Info("Snowflake resource released", "resource", resource.Name)
	return nil
}

// CleanupAllClaims is called at operator startup to release any per-claim state
// (Snowflake app user credentials, per-claim Secrets) left over from a previous run.
// It connects as admin to every configured account, revokes configured roles from the
// app user, and clears both RSA key slots.  It then deletes all per-claim Secrets
// (those whose name begins with "sf-appuser-") found in the operator namespace.
// Leases are released separately by the lease manager after this returns.
func (p *SnowflakeProvider) CleanupAllClaims(ctx context.Context, namespace string) error {
	log := logf.FromContext(ctx)
	log.Info("Running startup cleanup for Snowflake provider")

	// Connect to each configured account as admin and clean up the app user.
	for i := range p.config.Accounts {
		account := &p.config.Accounts[i]

		// Load admin credentials — use the direct API reader (cache not started yet)
		adminSecret := &corev1.Secret{}
		if err := p.reader.Get(ctx, client.ObjectKey{
			Name:      account.CredentialSecretName,
			Namespace: namespace,
		}, adminSecret); err != nil {
			log.Error(err, "Startup cleanup: failed to load admin secret, skipping account",
				"account", account.Name, "secret", account.CredentialSecretName)
			continue
		}

		adminUsername := string(adminSecret.Data["username"])
		adminPrivateKey := adminSecret.Data["privateKey"]
		if adminUsername == "" || len(adminPrivateKey) == 0 {
			log.Error(nil, "Startup cleanup: admin secret missing credentials, skipping account",
				"account", account.Name)
			continue
		}

		adminClient, err := NewClient(ctx, ConnectionParams{
			AccountURL: account.AccountURL,
			Username:   adminUsername,
			PrivateKey: adminPrivateKey,
		})
		if err != nil {
			log.Error(err, "Startup cleanup: failed to connect to Snowflake, skipping account",
				"account", account.Name)
			continue
		}

		for _, role := range account.AppUserRoles {
			adminClient.RevokeRole(ctx, role, appUser)
		}
		adminClient.ClearPublicKeys(ctx, appUser)
		if closeErr := adminClient.Close(); closeErr != nil {
			log.Error(closeErr, "Startup cleanup: failed to close admin client", "account", account.Name)
		}

		log.Info("Startup cleanup: cleared app user on account", "account", account.Name)
	}

	// Delete all per-claim Secrets (name prefix "sf-appuser-") in the operator namespace.
	// Use the direct API reader — cache not started yet.
	secretList := &corev1.SecretList{}
	if err := p.reader.List(ctx, secretList, client.InNamespace(namespace)); err != nil {
		log.Error(err, "Startup cleanup: failed to list secrets")
		return nil // non-fatal
	}

	for i := range secretList.Items {
		s := &secretList.Items[i]
		if !strings.HasPrefix(s.Name, "sf-appuser-") {
			continue
		}
		if err := p.client.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Startup cleanup: failed to delete app user secret", "secret", s.Name)
		} else {
			log.Info("Startup cleanup: deleted app user secret", "secret", s.Name)
		}
	}

	log.Info("Startup cleanup complete for Snowflake provider")
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
