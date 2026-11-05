#!/usr/bin/env bash
# Server-side validation on a precreated isolated namespace; no workload apply.
set -euo pipefail
namespace=${1:-ether035-ha}
[[ "$namespace" == ether035-ha* ]] || { echo 'use an isolated ether035-ha namespace' >&2; exit 64; }
helm=${HELM:-helm}
kubectl=${KUBECTL:-kubectl}
root=$(cd "$(dirname "$0")/../.." && pwd)
fixture=$(mktemp)
trap 'rm -f "$fixture"' EXIT
"$helm" template ether035 "$root/packaging/helm/ether" -n "$namespace" \
  -f "$root/packaging/kubernetes/mixed-cpu.values.yaml" \
  --set pools.server.replicas=1 --set cache.type=persistent \
  --set ingress.enabled=true --set 'ingress.hosts[0]=ether.example.test' \
  --set networkPolicy.enabled=true > "$fixture"
"$kubectl" -n "$namespace" apply --dry-run=server -f "$fixture"
