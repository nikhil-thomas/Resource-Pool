#!/bin/bash
set -e

KUBECONFIG_PATH="/Users/nikthoma/hacks/repositories/github.com/nikhil-thomas/Resource-Pool/kind-resource-pool"

echo "Using kubeconfig: $KUBECONFIG_PATH"

# Create secrets from private key files
kubectl --kubeconfig="$KUBECONFIG_PATH" create secret generic sf-cluster-1-creds \
  --from-literal=username=clusterpool_instance_bootstrap_appuser \
  --from-file=privateKey=clusters/data_and_ai_test_cluster_1/rsa_key.p8 \
  --namespace=default \
  --dry-run=client -o yaml > snowflake-secrets.yaml

echo "---" >> snowflake-secrets.yaml

kubectl --kubeconfig="$KUBECONFIG_PATH" create secret generic sf-cluster-2-creds \
  --from-literal=username=clusterpool_instance_bootstrap_appuser \
  --from-file=privateKey=clusters/data_and_ai_test_cluster_2/rsa_key.p8 \
  --namespace=default \
  --dry-run=client -o yaml >> snowflake-secrets.yaml

echo "✅ Generated snowflake-secrets.yaml"
echo ""
echo "To apply secrets to Kind cluster, run:"
echo "  kubectl --kubeconfig=\"$KUBECONFIG_PATH\" apply -f snowflake-secrets.yaml"
