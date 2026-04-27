# Resource Pool Operator Samples

This directory contains sample configurations for the Resource Pool Operator.

## Snowflake Provider

### 1. Configure Snowflake Accounts

Edit `snowflake-config.yaml` to match your Snowflake test accounts:

```yaml
accounts:
  - name: test-cluster-1
    accountUrl: your-account.snowflakecomputing.com
    warehouse: YOUR_WAREHOUSE
    database: YOUR_DATABASE
    schema: YOUR_SCHEMA
    role: YOUR_ROLE
    credentialSecretName: sf-test-cluster-1-creds
    labels:
      tier: standard
      region: us-east-1
    resetQueries:
      - "DROP TABLE IF EXISTS TEMP_TABLE_1"
```

Apply the configuration:
```bash
kubectl apply -f snowflake-config.yaml
```

### 2. Create Credential Secrets

Edit `snowflake-secrets.yaml` with your actual Snowflake credentials (username and RSA private key).

Apply the secrets:
```bash
kubectl apply -f snowflake-secrets.yaml
```

### 3. Create a ResourceClaim

Use `resourceclaim-snowflake.yaml` as a template:

```yaml
apiVersion: pool.dataverse.redhat.com/v1alpha1
kind: ResourceClaim
metadata:
  name: my-snowflake-claim
spec:
  type: snowflake
  leaseDuration: 2h
  requiredLabels:
    tier: standard
    region: us-east-1
```

Apply the claim:
```bash
kubectl apply -f resourceclaim-snowflake.yaml
```

### 4. Check Claim Status

```bash
# View all claims
kubectl get resourceclaims
kubectl get rc  # short form

# View detailed status
kubectl get resourceclaim my-snowflake-claim -o yaml

# Check connection details
kubectl get resourceclaim my-snowflake-claim -o jsonpath='{.status.connectionDetails}'

# Check leases
kubectl get leases -l pool.dataverse.redhat.com/resource-type=snowflake
```

### 5. Use the Resource

Once the claim is in `Bound` phase, you can use the connection details:

```bash
# Get connection details
kubectl get resourceclaim my-snowflake-claim -o yaml

# Example output:
# status:
#   phase: Bound
#   connectionDetails:
#     accountUrl: xy12345.us-east-1.snowflakecomputing.com
#     warehouse: COMPUTE_WH
#     database: TEST_DB
#     schema: PUBLIC
#     role: ACCOUNTADMIN
#   credentialSecretRef:
#     name: sf-test-cluster-1-creds
```

Use these details in your CI/CD pipeline or test scripts to connect to the Snowflake account.

### 6. Clean Up

Delete the claim to release the resource:

```bash
kubectl delete resourceclaim my-snowflake-claim
```

The operator will automatically:
- Execute reset queries to clean up test data
- Rotate the credentials
- Release the lease for the next claim

## Adding New Provider Types

To add support for a new resource type (e.g., PostgreSQL, AWS accounts):

1. Implement the `ResourceProvider` interface in `internal/provider/<type>/`
2. Register the provider in `cmd/main.go`
3. Create a ConfigMap with provider-specific configuration
4. Create corresponding Secrets with credentials
5. Create ResourceClaim with `spec.type: <your-provider-type>`

No changes to the core controller or lease manager are needed!
