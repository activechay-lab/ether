#!/usr/bin/env bash
# Abruptly delete one role pod by UID. StatefulSet recreates it; no graceful drain.
set -euo pipefail
state=${ETHER_LAB_STATE:-/nonexistent}
source "$(dirname "$0")/lab-config.sh"
pod=${1:?usage: role-loss.sh POD}
[[ $pod =~ ^${prefix}-ether-(server|source|transcode|delivery)-[0-9]+$ ]] || exit 64
[[ $(kubectl config current-context) == "$context" ]] || exit 64
read -r uid role <<< "$(kubectl -n "$namespace" get pod "$pod" -o 'jsonpath={.metadata.uid}{" "}{.metadata.labels.ether\.plex/role}')"
[[ $uid =~ ^[a-f0-9-]{36}$ && $role =~ ^(server|source|transcode|delivery)$ ]] || exit 64
# gracePeriodSeconds=0 is the API equivalent of --grace-period=0 --force;
# UID precondition prevents deleting a replacement that raced this request.
printf '{"apiVersion":"v1","kind":"DeleteOptions","gracePeriodSeconds":0,"preconditions":{"uid":"%s"}}' "$uid" |
 kubectl delete --raw "/api/v1/namespaces/$namespace/pods/$pod" -f - >/dev/null
printf 'forced role=%s pod=%s uid=%s\n' "$role" "$pod" "$uid"
