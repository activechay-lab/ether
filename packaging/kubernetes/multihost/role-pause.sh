#!/usr/bin/env bash
# Freeze exactly one owned Ether container and its descendants, restore on exit.
set -euo pipefail
pod=${1:?usage: role-pause.sh POD VERIFIER [ARGS...]}; shift
(( $# > 0 )) || exit 64
[[ $(kubectl config current-context) == ether035-multihost ]] || exit 64
[[ $pod =~ ^ether035-mh-ether-(server|source|transcode|delivery)-[0-9]+$ ]] || exit 64
read -r uid node task <<< "$(kubectl -n ether035-ha get pod "$pod" -o 'jsonpath={.metadata.uid}{" "}{.spec.nodeName}{" "}{.status.containerStatuses[?(@.name=="ether")].containerID}')"
task=${task#containerd://}
[[ $task =~ ^[a-f0-9]{64}$ && $node =~ ^ether035-mh-(server|agent[12])$ ]] || exit 64
read -r cid label <<< "$(docker inspect --format '{{.Id}} {{index .Config.Labels "ether.lab"}}' "$node")"
[[ $label == ether035-multihost ]] || exit 64
active=false
restore() { if $active; then docker exec "$cid" ctr -n k8s.io tasks resume "$task"; fi; }
trap restore EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Container ID is immutable; a replacement cannot be paused by name reuse.
active=true
docker exec "$cid" ctr -n k8s.io tasks pause "$task"
printf 'paused pod=%s uid=%s task=%s node=%s\n' "$pod" "$uid" "$task" "$node"
"$@"
