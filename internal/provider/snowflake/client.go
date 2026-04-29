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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
	log := logf.FromContext(ctx)

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
		_ = db.Close()
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
	log := logf.FromContext(ctx)

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
	log := logf.FromContext(ctx)

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

// EnsureUser creates the Snowflake user if it does not already exist.
func (c *Client) EnsureUser(ctx context.Context, username, role, warehouse string) error {
	log := logf.FromContext(ctx)
	log.Info("Ensuring Snowflake user exists", "username", username)

	query := fmt.Sprintf(
		"CREATE USER IF NOT EXISTS %s DEFAULT_ROLE = %s DEFAULT_WAREHOUSE = %s",
		username, role, warehouse,
	)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to create user %s: %w", username, err)
	}

	log.Info("User ready", "username", username)
	return nil
}

// SetPublicKey sets RSA_PUBLIC_KEY (slot 1) for the given user and clears slot 2.
func (c *Client) SetPublicKey(ctx context.Context, username, publicKey string) error {
	log := logf.FromContext(ctx)
	log.Info("Setting public key for user", "username", username)

	setKey := fmt.Sprintf("ALTER USER %s SET RSA_PUBLIC_KEY='%s'", username, publicKey)
	if _, err := c.db.ExecContext(ctx, setKey); err != nil {
		return fmt.Errorf("failed to set public key for %s: %w", username, err)
	}

	clearKey2 := fmt.Sprintf("ALTER USER %s UNSET RSA_PUBLIC_KEY_2", username)
	if _, err := c.db.ExecContext(ctx, clearKey2); err != nil {
		// Non-fatal: slot 2 may already be empty
		log.Error(err, "Failed to clear RSA_PUBLIC_KEY_2 (may be already empty)", "username", username)
	}

	log.Info("Public key set, key slot 2 cleared", "username", username)
	return nil
}

// GrantRole grants a Snowflake role to the given user.
func (c *Client) GrantRole(ctx context.Context, role, username string) error {
	log := logf.FromContext(ctx)
	log.Info("Granting role to user", "role", role, "username", username)

	query := fmt.Sprintf("GRANT ROLE %s TO USER %s", role, username)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to grant role %s to %s: %w", role, username, err)
	}

	log.Info("Role granted", "role", role, "username", username)
	return nil
}

// RevokeRole revokes a Snowflake role from the given user.
// Non-fatal if the role is not currently granted — error is logged only.
func (c *Client) RevokeRole(ctx context.Context, role, username string) {
	log := logf.FromContext(ctx)
	log.Info("Revoking role from user", "role", role, "username", username)

	query := fmt.Sprintf("REVOKE ROLE %s FROM USER %s", role, username)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		log.Error(err, "Failed to revoke role (may not be granted)", "role", role, "username", username)
		return
	}

	log.Info("Role revoked", "role", role, "username", username)
}

// ClearPublicKeys unsets both RSA_PUBLIC_KEY slots for the given user.
// Both operations are best-effort; errors are logged but not returned since
// slots may already be empty.
func (c *Client) ClearPublicKeys(ctx context.Context, username string) {
	log := logf.FromContext(ctx)
	log.Info("Clearing RSA public keys for user", "username", username)

	for _, stmt := range []string{
		fmt.Sprintf("ALTER USER %s UNSET RSA_PUBLIC_KEY", username),
		fmt.Sprintf("ALTER USER %s UNSET RSA_PUBLIC_KEY_2", username),
	} {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			log.Error(err, "Failed to unset key slot (may already be empty)", "username", username, "stmt", stmt)
		}
	}

	log.Info("RSA key slots cleared", "username", username)
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
