#!/usr/bin/env bash
# Replace one explicitly selected Ether pod, preserving its ordinal/PVC.
set -euo pipefail
if [[ $# != 2 ]]; then echo "usage: $0 namespace pod" >&2; exit 64; fi
namespace=$1
pod=$2
kubectl=${KUBECTL:-kubectl}
role=$("$kubectl" -n "$namespace" get pod "$pod" -o 'jsonpath={.metadata.labels.ether\.plex/role}')
case "$role" in full|server|source|transcode|delivery) ;; *) echo 'not an Ether role pod' >&2; exit 64;; esac
old_uid=$("$kubectl" -n "$namespace" get pod "$pod" -o 'jsonpath={.metadata.uid}')
# Do not delete on drain timeout. The runtime retains unresolved ownership.
if "$kubectl" -n "$namespace" exec "$pod" -c ether -- /usr/lib/ether/ether cluster drain --data-dir /config --wait 120s; then
  :
else
  drain_status=$?
  echo 'drain failed; pod retained, unresolved work must remain fenced' >&2
  exit "$drain_status"
fi
"$kubectl" -n "$namespace" delete pod "$pod" --wait=true --timeout=180s
# wait Ready may observe the old pod or a gap unless we check replacement UID.
for ((i=0; i<120; i++)); do
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
