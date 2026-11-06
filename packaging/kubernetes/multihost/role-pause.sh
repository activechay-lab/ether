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
restore() {
 $active || return 0
 if docker exec "$cid" ctr -n k8s.io tasks resume "$task"; then
  printf 'restored original task=%s\n' "$task"
  return 0
 fi
 # Kubelet may have replaced a frozen task. Never resume by pod/name lookup.
 local tasks
 tasks=$(docker exec "$cid" ctr -n k8s.io tasks ls) || return 1
 if awk -v id="$task" 'NR>1 && $1==id {found=1} END {exit !found}' <<< "$tasks"; then
  echo 'original task still exists but could not resume' >&2
  return 1
 fi
 for ((attempt=0; attempt<60; attempt++)); do
  local current_uid current_task ready
  if read -r current_uid current_task ready <<< "$(kubectl -n ether035-ha get pod "$pod" -o 'jsonpath={.metadata.uid}{" "}{.status.containerStatuses[?(@.name=="ether")].containerID}{" "}{.status.containerStatuses[?(@.name=="ether")].ready}')"; then
   current_task=${current_task#containerd://}
   if [[ $current_task =~ ^[a-f0-9]{64}$ && $current_task != "$task" && $ready == true ]]; then
    printf 'restored by kubelet: original_task_absent=%s replacement_uid=%s replacement_task=%s ready=true\n' "$task" "$current_uid" "$current_task"
    # This is restoration evidence only, not source/encoder retirement proof.
    return 0
   fi
  fi
  sleep 2
 done
 echo 'original task absent; replacement did not become Ready' >&2
 return 1
}
trap restore EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Container ID is immutable; a replacement cannot be paused by name reuse.
active=true
docker exec "$cid" ctr -n k8s.io tasks pause "$task"
printf 'paused pod=%s uid=%s task=%s node=%s\n' "$pod" "$uid" "$task" "$node"
"$@"
