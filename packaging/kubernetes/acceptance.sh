#!/usr/bin/env bash
# The executable verifier is supplied by the playback acceptance owner.
# It must exit nonzero on continuity/fencing/retirement failure. No fake fallback.
set -euo pipefail
if [[ $# != 4 ]]; then
  echo "usage: $0 namespace pod verifier-executable output-directory" >&2
  exit 64
fi
namespace=$1 pod=$2 verifier=$3 output=$4
[[ "$namespace" == ether035-ha* ]] || { echo 'only isolated ether035-ha namespaces allowed' >&2; exit 64; }
[[ -x "$verifier" ]] || { echo 'real playback verifier is required' >&2; exit 64; }
kubectl=${KUBECTL:-kubectl}
mkdir -p "$output"
# Never capture Secrets, deployment config bytes, identities, tokens or logs.
$kubectl -n "$namespace" get pods,pvc -o wide > "$output/before.txt"
$kubectl -n "$namespace" get pod "$pod" -o 'jsonpath={.metadata.uid}' > "$output/old-pod-uid.txt"
$kubectl -n "$namespace" get pvc -o 'custom-columns=NAME:.metadata.name,UID:.metadata.uid,VOLUME:.spec.volumeName' > "$output/pvc-before.txt"
"$verifier" before "$namespace" "$pod" "$output"
# during must run long enough to overlap replacement and collect stream metrics.
"$verifier" during "$namespace" "$pod" "$output" &
verifier_pid=$!
cleanup() {
  if [[ -n "${verifier_pid:-}" ]]; then
    # This is the verifier child started above, never another user's process.
    kill -TERM "$verifier_pid" 2>/dev/null || true
    wait "$verifier_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM
KUBECTL="$kubectl" "$(dirname "$0")/rollout.sh" "$namespace" "$pod" > "$output/replacement.txt"
wait "$verifier_pid"
verifier_pid=
"$verifier" after "$namespace" "$pod" "$output"
$kubectl -n "$namespace" get pods,pvc -o wide > "$output/after.txt"
$kubectl -n "$namespace" get pvc -o 'custom-columns=NAME:.metadata.name,UID:.metadata.uid,VOLUME:.spec.volumeName' > "$output/pvc-after.txt"
diff -u "$output/pvc-before.txt" "$output/pvc-after.txt"
echo 'graceful replacement and workload verifier passed; hard loss requires separate fencing acceptance'
