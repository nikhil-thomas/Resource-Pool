# Local VSCode Debugging Setup

This guide explains how to run and debug the Resource Pool Operator locally in VSCode while connected to a Kind cluster.

## Prerequisites

- VSCode with Go extension installed
- Kind cluster running (with kubeconfig at repository root)
- kubectl installed
- Go 1.23+ installed

## Setup Steps

### 1. Verify Kind Cluster is Running

```bash
kubectl --kubeconfig=kind-resource-pool cluster-info
```

Expected output should show the cluster at `https://127.0.0.1:54384`

### 2. Install CRDs

```bash
kubectl --kubeconfig=kind-resource-pool apply -f config/crd/bases/pool.dataverse.redhat.com_resourcebids.yaml
```

Verify:
```bash
kubectl --kubeconfig=kind-resource-pool get crd resourcebids.pool.dataverse.redhat.com
```

### 3. Generate and Apply Secrets

```bash
cd config/kind
chmod +x create-secrets.sh
./create-secrets.sh
kubectl --kubeconfig=../../kind-resource-pool apply -f snowflake-secrets.yaml
cd ../..
```

Verify:
```bash
kubectl --kubeconfig=kind-resource-pool get secrets -n default | grep sf-cluster
```

Expected output:
```
sf-cluster-1-creds   Opaque   2      1m
sf-cluster-2-creds   Opaque   2      1m
```

### 4. Apply Snowflake Configuration

```bash
kubectl --kubeconfig=kind-resource-pool apply -f config/kind/snowflake-config.yaml
```

Verify:
```bash
kubectl --kubeconfig=kind-resource-pool get cm snowflake-config -o yaml
```

### 5. Start Debugging in VSCode

1. Open VSCode
2. Go to Run and Debug view (Cmd+Shift+D on Mac)
3. Select "Debug Resource Pool Operator" from the dropdown
4. Click the green play button or press F5

The operator will start locally and connect to your Kind cluster.

**Expected startup logs:**
```
INFO    setup   Registered providers    {"providers": ["snowflake"]}
INFO    setup   Provider initialized    {"provider": "snowflake", "resourceCount": 2}
INFO    setup   Leases initialized      {"provider": "snowflake", "leaseCount": 2}
INFO    setup   Starting manager
```

### 6. Verify Leases Were Created

In a separate terminal:
```bash
kubectl --kubeconfig=kind-resource-pool get leases
```

Expected output:
```
NAME                                                    HOLDER   AGE
resource-lease-snowflake-data-and-ai-test-cluster-1             5s
resource-lease-snowflake-data-and-ai-test-cluster-2             5s
```

### 7. Create a Test ResourceBid

While the operator is running in debug mode:

```bash
kubectl --kubeconfig=kind-resource-pool apply -f config/kind/test-resourcebid.yaml
```

**Watch the operator logs** in VSCode's Debug Console. You should see:
```
INFO    Reconciling ResourceBid   {"resourcebid": "default/test-bid"}
INFO    Found free lease            {"lease": "resource-lease-snowflake-data-and-ai-test-cluster-1"}
INFO    Acquired lease              {"bid": "default/test-bid", "lease": "resource-lease-snowflake-data-and-ai-test-cluster-1"}
INFO    ResourceBid bound         {"bid": "default/test-bid", "phase": "Bound"}
```

### 8. Check Bid Status

```bash
kubectl --kubeconfig=kind-resource-pool get resourcebid test-bid -o yaml
```

Expected status:
```yaml
status:
  phase: Bound
  leaseName: resource-lease-snowflake-data-and-ai-test-cluster-1
  connectionDetails:
    accountUrl: gdadclc-data_and_ai_test_cluster_1.snowflakecomputing.com
    warehouse: clusterpool_instance_bootstrap_warehouse
    database: ""
    schema: ""
    role: clusterpool_instance_bootstrap_role
  credentialSecretRef:
    name: sf-cluster-1-creds
```

### 9. Test Cleanup by Deleting Bid

```bash
kubectl --kubeconfig=kind-resource-pool delete resourcebid test-bid
```

**Watch operator logs** for cleanup process:
```
INFO    Finalizer cleanup started   {"bid": "default/test-bid"}
INFO    Executing reset queries     {"bid": "default/test-bid", "queries": 3}
INFO    Rotating credentials        {"bid": "default/test-bid"}
INFO    Lease released              {"lease": "resource-lease-snowflake-data-and-ai-test-cluster-1"}
```

Verify lease is released:
```bash
kubectl --kubeconfig=kind-resource-pool get lease resource-lease-snowflake-data-and-ai-test-cluster-1 -o yaml
```

The `spec.holderIdentity` should be empty.

## Debugging Tips

### Setting Breakpoints

1. Open files like [internal/controller/resourcebid_controller.go](../../internal/controller/resourcebid_controller.go)
2. Click in the left margin to set breakpoints
3. Create a ResourceBid to trigger reconciliation
4. Step through code using VSCode debugger controls

### Useful Breakpoint Locations

- [internal/controller/resourcebid_controller.go:Reconcile](../../internal/controller/resourcebid_controller.go) - Entry point for reconciliation
- [internal/lease/manager.go:AcquireFreeLease](../../internal/lease/manager.go) - Lease acquisition logic
- [internal/provider/snowflake/provider.go:ReleaseResource](../../internal/provider/snowflake/provider.go) - Cleanup and rotation

### Watching Resources

In a separate terminal, watch resources in real-time:

```bash
# Watch all ResourceBids
kubectl --kubeconfig=kind-resource-pool get resourcebids -w

# Watch all Leases
kubectl --kubeconfig=kind-resource-pool get leases -w
```

### Restarting the Operator

1. Stop debugging (Shift+F5)
2. Start debugging again (F5)
3. Leases will be reinitialized if they don't exist

## Troubleshooting

### "Failed to get API Group-Resources"

**Symptom:** Operator crashes on startup with API discovery errors.

**Solution:** Make sure CRDs are installed:
```bash
kubectl --kubeconfig=kind-resource-pool apply -f config/crd/bases/
```

### "ConfigMap 'snowflake-config' not found"

**Symptom:** Provider initialization fails.

**Solution:**
```bash
kubectl --kubeconfig=kind-resource-pool apply -f config/kind/snowflake-config.yaml
```

### "Secret 'sf-cluster-X-creds' not found"

**Symptom:** Lease initialization fails or ReleaseResource fails.

**Solution:**
```bash
cd config/kind
./create-secrets.sh
kubectl --kubeconfig=../../kind-resource-pool apply -f snowflake-secrets.yaml
```

### "connection refused" to localhost:54384

**Symptom:** Operator can't connect to Kind cluster.

**Solution:** Verify Kind cluster is running:
```bash
kind get clusters
docker ps | grep resource-pool
```

If cluster is not running:
```bash
kind create cluster --name resource-pool-kind --kubeconfig kind-resource-pool
```

### Breakpoints not hitting

**Solution:**
1. Make sure you're in Debug mode (not Run mode)
2. Verify breakpoint has a red dot (not gray)
3. Trigger reconciliation by creating/updating a ResourceBid
4. Check Debug Console for any errors

## Clean Up

### Delete Test Resources

```bash
kubectl --kubeconfig=kind-resource-pool delete resourcebid test-bid
kubectl --kubeconfig=kind-resource-pool delete leases -l pool.dataverse.redhat.com/resource-type=snowflake
```

### Stop Operator

Press Shift+F5 in VSCode or click the red square stop button.

### Delete Kind Cluster (optional)

```bash
kind delete cluster --name resource-pool-kind
```
