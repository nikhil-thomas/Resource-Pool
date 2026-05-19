# Snowflake Resource Pool Operator

A Kubernetes operator for managing a pool of Snowflake test clusters using Kubernetes Leases for coordination.

## Overview

This operator enables teams to share a limited pool of Snowflake test accounts across multiple CI/CD pipelines without conflicts. Users create a `ResourceBid` custom resource, and the operator automatically assigns a free Snowflake account, providing connection details. When the bid is deleted, the operator resets the Snowflake state, rotates credentials, and releases the resource back to the pool.

## Key Features

- **Lease-based coordination**: Uses Kubernetes Leases for distributed locking
- **Automatic cleanup**: Finalizers ensure Snowflake state is reset even on force-delete
- **Credential rotation**: Automatically rotates RSA keys when resources are released
- **Label-based filtering**: Route bids to specific account tiers/regions
- **GitLab CI/CD friendly**: Easy integration with test pipelines

## Quick Start

See [IMPLEMENTATION.md](./IMPLEMENTATION.md) for the complete step-by-step implementation guide.

### Prerequisites

- Kubernetes cluster (1.24+)
- Go 1.21+
- Kubebuilder 3.13+
- Snowflake accounts with service account credentials

### Installation

```bash
# Initialize the project
kubebuilder init --domain snowflake.io --repo github.com/nikhil-thomas/Resource-Pool

# Create the ResourceBid CRD
kubebuilder create api --group pool --version v1alpha1 --kind ResourceBid --resource --controller

# Generate manifests
make manifests

# Deploy to cluster
make deploy IMG=snowflake-pool-operator:latest
```

### Usage

1. **Configure the pool** - Create a ConfigMap with your Snowflake accounts:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: snowflake-accounts
data:
  accounts.yaml: |
    accounts:
      - name: test-cluster-1
        accountUrl: xy12345.us-east-1.snowflakecomputing.com
        warehouse: COMPUTE_WH
        credentialSecretName: sf-test-cluster-1-creds
        labels:
          tier: standard
```

2. **Create credential secrets** for each account

3. **Bid for a resource**:

```yaml
apiVersion: pool.snowflake.io/v1alpha1
kind: ResourceBid
metadata:
  name: my-test
spec:
  leaseDuration: 2h
  requiredLabels:
    tier: standard
```

4. **Get connection details**:

```bash
kubectl get resourcebid my-test -o jsonpath='{.status.connectionDetails}'
```

5. **Release when done**:

```bash
kubectl delete resourcebid my-test
```

## Architecture

```
┌─────────────┐
│ ResourceBid │──────┐
└─────────────┘      │
                     ▼
             ┌───────────────┐      ┌────────┐
             │   Operator    │─────▶│ Leases │
             │  (Reconcile)  │      └────────┘
             └───────────────┘
                     │
                     ▼
             ┌───────────────┐
             │   Snowflake   │
             │   Accounts    │
             └───────────────┘
```

## Documentation

- **[IMPLEMENTATION.md](./IMPLEMENTATION.md)** - Complete implementation guide with code examples
- **Architecture diagram** - See [Ephemeral Snowflake Cluster Pool Manager Operator.png](./Ephemeral%20Snowflake%20Cluster%20Pool%20Manager%20Operator.png)

## Development Status

⚠️ **In Development** - This project is currently being implemented. See [IMPLEMENTATION.md](./IMPLEMENTATION.md) for the implementation plan.

## Project Structure

```
Resource-Pool/
├── api/v1alpha1/              # CRD definitions
│   └── resourcebid_types.go
├── internal/
│   ├── controller/            # Reconciliation logic
│   ├── config/               # Configuration loading
│   ├── lease/                # Lease management
│   └── snowflake/            # Snowflake client & crypto
├── config/
│   ├── crd/                  # Generated CRD manifests
│   ├── samples/              # Example configurations
│   └── rbac/                 # RBAC rules
└── IMPLEMENTATION.md         # Detailed implementation guide
```

## Contributing

This is a work-in-progress project. Contributions welcome!

## License

See [LICENSE](./LICENSE) file for details.
