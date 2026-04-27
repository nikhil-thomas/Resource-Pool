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

// SnowflakeConfig is loaded from ConfigMap at operator startup
type SnowflakeConfig struct {
	Accounts []SnowflakeAccount `yaml:"accounts"`
}

// SnowflakeAccount represents one Snowflake test account/cluster in the pool
type SnowflakeAccount struct {
	// Name is a unique identifier for this account (used in lease name)
	Name string `yaml:"name"`

	// AccountURL is the full Snowflake account URL
	// Example: "xy12345.us-east-1.snowflakecomputing.com"
	AccountURL string `yaml:"accountUrl"`

	// Warehouse is the Snowflake compute warehouse to use
	Warehouse string `yaml:"warehouse"`

	// Database is the default database (optional)
	Database string `yaml:"database"`

	// Schema is the default schema (optional)
	Schema string `yaml:"schema"`

	// Role is the Snowflake role to assume
	Role string `yaml:"role"`

	// CredentialSecretName is the name of the K8s Secret containing credentials
	// Secret must have keys: "username" and "privateKey"
	CredentialSecretName string `yaml:"credentialSecretName"`

	// Labels allow filtering which accounts can be assigned to claims
	// Example: {"tier": "standard", "region": "us-east-1"}
	Labels map[string]string `yaml:"labels"`

	// AppUserRoles are the Snowflake roles to grant to the app user (test_runner_appuser)
	// on acquire and revoke on release.
	// Example: ["ACCOUNTADMIN"]
	AppUserRoles []string `yaml:"appUserRoles,omitempty"`

	// ResetQueries are SQL statements to run during cleanup
	// Example: ["DROP TABLE IF EXISTS TEMP_TABLE_1", "DROP SCHEMA IF EXISTS TEMP_SCHEMA CASCADE"]
	ResetQueries []string `yaml:"resetQueries,omitempty"`
}

// MatchesLabels checks if account labels satisfy required labels
func (a *SnowflakeAccount) MatchesLabels(required map[string]string) bool {
	if len(required) == 0 {
		return true // no requirements = matches all
	}

	for key, requiredValue := range required {
		accountValue, exists := a.Labels[key]
		if !exists || accountValue != requiredValue {
			return false
		}
	}

	return true
}
