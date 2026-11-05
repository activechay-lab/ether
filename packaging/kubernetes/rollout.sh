#!/usr/bin/env bash
# Replace one explicitly selected Ether pod, preserving its ordinal/PVC.
set -euo pipefail
if [[ $# != 2 ]]; then echo "usage: $0 namespace pod" >&2; exit 64; fi
namespace=$1
pod=$2
kubectl=${KUBECTL:-kubectl}
[[ "$namespace" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ && "$pod" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]] || exit 64
read -r role old_uid data_dir <<< "$("$kubectl" -n "$namespace" get pod "$pod" -o 'jsonpath={.metadata.labels.ether\.plex/role}{"\t"}{.metadata.uid}{"\t"}{.spec.containers[?(@.name=="ether")].env[?(@.name=="ETHER_DATA_DIR")].value}')"
case "$role" in full|server|source|transcode|delivery) ;; *) echo 'not an Ether role pod' >&2; exit 64;; esac
[[ "$old_uid" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$ ]] || { echo 'invalid pod UID' >&2; exit 64; }
case "$data_dir" in /config|/config/ether) ;; *) echo 'unsupported pod data directory' >&2; exit 64;; esac
# Do not delete on drain timeout. The runtime retains unresolved ownership.
if "$kubectl" -n "$namespace" exec "$pod" -c ether -- /usr/lib/ether/ether cluster drain --data-dir "$data_dir" --wait 120s; then
  :
else
  drain_status=$?
  echo 'drain failed; pod retained, unresolved work must remain fenced' >&2
  exit "$drain_status"
fi
# The API atomically rejects a replacement UID. A second GET before ordinary
# delete would still race; never retry a conflict or delete again by name.
# Omit gracePeriodSeconds so the API preserves the pod's normal grace period.
printf '{"apiVersion":"v1","kind":"DeleteOptions","preconditions":{"uid":"%s"}}' "$old_uid" |
  "$kubectl" delete --raw "/api/v1/namespaces/$namespace/pods/$pod" -f - > /dev/null
# Raw DELETE returns when accepted. Wait for the old UID to disappear and a
# replacement to become Ready; do not interpret the old pod's Ready as success.
for ((i=0; i<240; i++)); do
  new_uid=$("$kubectl" -n "$namespace" get pod "$pod" -o 'jsonpath={.metadata.uid}' 2>/dev/null || true)
  if [[ -n "$new_uid" && "$new_uid" != "$old_uid" ]]; then
    "$kubectl" -n "$namespace" wait --for=condition=Ready "pod/$pod" --timeout=600s
    printf 'replacement ready: %s uid=%s previous=%s\n' "$pod" "$new_uid" "$old_uid"
    exit 0
  fi
  sleep 1
done
echo 'replacement did not appear; inspect StatefulSet events' >&2
exit 1
