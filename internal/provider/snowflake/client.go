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
	"database/sql"
	"fmt"
	"strings"

	sf "github.com/snowflakedb/gosnowflake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Client wraps Snowflake database connection
type Client struct {
	db       *sql.DB
	username string
}

// ConnectionParams contains all parameters needed to connect to Snowflake
type ConnectionParams struct {
	AccountURL string
	Username   string
	Role       string
	Warehouse  string
	Database   string
	Schema     string
	PrivateKey []byte
}

// NewClient creates a new Snowflake client
func NewClient(ctx context.Context, params ConnectionParams) (*Client, error) {
	log := log.FromContext(ctx)

	// Parse private key
	privateKey, err := ParsePrivateKey(params.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Extract account identifier from URL
	// Example: "xy12345.us-east-1.snowflakecomputing.com" -> "xy12345.us-east-1"
	account := extractAccountFromURL(params.AccountURL)

	// Configure Snowflake connection
	config := &sf.Config{
		Account:       account,
		User:          params.Username,
		Authenticator: sf.AuthTypeJwt,
		PrivateKey:    privateKey,
		Role:          params.Role,
		Warehouse:     params.Warehouse,
		Database:      params.Database,
		Schema:        params.Schema,
	}

	dsn, err := sf.DSN(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create DSN: %w", err)
	}

	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open connection: %w", err)
	}

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping Snowflake: %w", err)
	}

	log.Info("Connected to Snowflake",
		"account", account,
		"username", params.Username,
		"warehouse", params.Warehouse)

	return &Client{
		db:       db,
		username: params.Username,
	}, nil
}

// extractAccountFromURL extracts account identifier from Snowflake URL
// Handles various URL formats:
// - "xy12345.us-east-1.snowflakecomputing.com" -> "xy12345.us-east-1"
// - "https://xy12345.us-east-1.snowflakecomputing.com" -> "xy12345.us-east-1"
// - "xy12345" -> "xy12345"
func extractAccountFromURL(accountURL string) string {
	// Remove protocol if present
	accountURL = strings.TrimPrefix(accountURL, "https://")
	accountURL = strings.TrimPrefix(accountURL, "http://")

	// Remove .snowflakecomputing.com suffix if present
	accountURL = strings.TrimSuffix(accountURL, ".snowflakecomputing.com")

	return accountURL
}

// ResetState cleans up the Snowflake environment
// Executes custom reset queries defined in account configuration
func (c *Client) ResetState(ctx context.Context, resetQueries []string) error {
	log := log.FromContext(ctx)

	if len(resetQueries) == 0 {
		log.Info("No reset queries configured, skipping reset")
		return nil
	}

	log.Info("Resetting Snowflake state", "queryCount", len(resetQueries))

	for i, query := range resetQueries {
		log.V(1).Info("Executing reset query", "index", i, "query", query)

		_, err := c.db.ExecContext(ctx, query)
		if err != nil {
			// Log error but continue with other queries
			// This is best-effort cleanup
			log.Error(err, "Reset query failed", "index", i, "query", query)
			// Depending on requirements, you might want to return error here
			// For now, we'll continue with cleanup even if one query fails
		}
	}

	log.Info("Snowflake state reset completed")
	return nil
}

// RotateCredentials updates the RSA public key for the user
func (c *Client) RotateCredentials(ctx context.Context, newPublicKey string) error {
	log := log.FromContext(ctx)

	log.Info("Rotating credentials for user", "username", c.username)

	// Snowflake SQL to update public key
	query := fmt.Sprintf("ALTER USER %s SET RSA_PUBLIC_KEY='%s'",
		c.username,
		newPublicKey)

	_, err := c.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to rotate credentials: %w", err)
	}

	log.Info("Credentials rotated successfully", "username", c.username)
	return nil
}

// ExecuteQuery runs an arbitrary SQL query (useful for testing/validation)
func (c *Client) ExecuteQuery(ctx context.Context, query string) error {
	_, err := c.db.ExecContext(ctx, query)
	return err
}

// Close closes the database connection
func (c *Client) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}
