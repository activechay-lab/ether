#!/usr/bin/env bash
# Isolated kind worker process-loss harness. No arbitrary Docker container target.
# Only use with a real cross-node playback/fencing verifier.
set -euo pipefail
if [[ $# != 4 ]]; then
  echo "usage: $0 namespace ether035-ha-worker[N] verifier-executable output-directory" >&2
  exit 64
fi
namespace=$1 node=$2 verifier=$3 output=$4
[[ "$namespace" == ether035-ha* ]] || exit 64
[[ "$node" =~ ^ether035-ha-worker[0-9]*$ ]] || exit 64
[[ -x "$verifier" ]] || { echo 'real playback verifier required' >&2; exit 64; }
kubectl=${KUBECTL:-kubectl}
# Capture ID and authorization labels in one response for the same object.
# Every later Docker operation uses that immutable ID, even if its name is reused.
read -r container_id cluster kind_role <<< "$(docker inspect --format '{{.Id}} {{index .Config.Labels "io.x-k8s.kind.cluster"}} {{index .Config.Labels "io.x-k8s.kind.role"}}' "$node")"
[[ "$container_id" =~ ^[a-f0-9]{64}$ && "$cluster" == ether035-ha && "$kind_role" == worker ]] || {
  echo 'container is not an authorized lab worker' >&2; exit 64;
}
mkdir -p "$output"
printf '%s\n' "$container_id" > "$output/lost-container-id.txt"
"$kubectl" -n "$namespace" get pods -o wide > "$output/before-loss.txt"
"$verifier" before-loss "$namespace" "$node" "$output"
verifier_pid=
restore_node=false
cleanup() {
  if $restore_node; then docker start "$container_id" > /dev/null; fi
  if [[ -n "$verifier_pid" ]]; then
    kill -TERM "$verifier_pid" 2>/dev/null || true
    wait "$verifier_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM
"$verifier" during-loss "$namespace" "$node" "$output" &
verifier_pid=$!
restore_node=true
# Captured ID and cluster label checked above; never Docker wildcard/prune.
docker stop --time 0 "$container_id" > /dev/null
docker inspect --format 'id={{.Id}} running={{.State.Running}} pid={{.State.Pid}} finished={{.State.FinishedAt}}' \
  "$container_id" > "$output/termination-proof.txt"
[[ $(docker inspect --format '{{.State.Running}}/{{.State.Pid}}' "$container_id") == false/0 ]] || exit 1
# The verifier observes real media and independent authority while this node is
# stopped. It must prove source/encoder fencing, not infer it from pod silence.
wait "$verifier_pid"
verifier_pid=
"$verifier" after-loss "$namespace" "$node" "$output"
docker start "$container_id" > /dev/null
restore_node=false
"$kubectl" wait --for=condition=Ready "node/$node" --timeout=300s
"$verifier" after-recovery "$namespace" "$node" "$output"
"$kubectl" -n "$namespace" get pods,pvc -o wide > "$output/after-recovery.txt"
echo 'node process loss and real workload verifier passed'
