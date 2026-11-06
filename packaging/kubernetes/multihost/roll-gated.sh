#!/usr/bin/env bash
# Operator invokes only after receiving the exact green integration image tag.
# heavy-io.sh owns serialization and reports queue+run wall time.
set -euo pipefail
state=${1:?usage: roll-gated.sh PRIVATE_STATE GATED_IMAGE_TAG}
tag=${2:?gated image tag required}
[[ ${ETHER_HEAVY_IO_LOCKED:-0} == 1 ]] || { echo 'invoke through heavy-io.sh' >&2; exit 64; }
source "$(dirname "$0")/lab-config.sh"
case "$lab:$context:$prefix:$namespace" in
 ether035-multihost:ether035-multihost:ether035-mh:ether035-ha|ether035-lab2:ether035-lab2:ether035-lab2:ether035-lab2) ;;
 *) echo 'only approved isolated lab tuples are allowed' >&2; exit 64;;
esac
export ETHER_LAB_PREFIX="$prefix" ETHER_LAB_NAMESPACE="$namespace"
root=$(cd "$(dirname "$0")/../../.." && pwd)
chart=${ETHER_LAB_CHART:-$root/packaging/helm/ether}
[[ -f $chart/Chart.yaml ]] || { echo 'Helm chart not found' >&2; exit 64; }
export KUBECONFIG="$state/kubeconfig" PATH="$state/bin:$PATH" MULTIHOST_IP
[[ $(kubectl config current-context) == "$context" ]] || exit 64
if [[ ${ETHER_LAB_IMAGES_IMPORTED:-0} != 1 ]]; then "$root/packaging/kubernetes/multihost/import.sh" "$state" "ether035-multihost:$tag"; fi
extra=(); post=()
if [[ -s $state/physical-host.values.yaml ]]; then extra=(-f "$state/physical-host.values.yaml"); fi
if [[ -s $state/external-private-ip ]]; then
 MULTIHOST_IP=$(cat "$state/external-private-ip")
 post=(--post-renderer "$root/packaging/kubernetes/multihost/external-private.py")
fi
kubectl -n "$namespace" get pods -o json > "$state/pods-before-gated-roll.json"
tmpfs=$(kubectl -n "$namespace" get statefulset "${namespace}-pg" -o json | python3 -c '
import json,sys
s=json.load(sys.stdin)["spec"]["template"]["spec"]
c=next(c for c in s["containers"] if c["name"]=="postgres")
name=next(v["name"] for v in c["volumeMounts"] if v["mountPath"]=="/var/lib/postgresql/data")
print(next(v for v in s["volumes"] if v["name"]==name).get("emptyDir",{}).get("medium",""))')
if [[ $tmpfs != Memory ]]; then
 # Quiesce all writers before a complete PG snapshot; old PVCs/identities remain.
 for role in server source transcode delivery; do
  for ordinal in 0 1; do
   kubectl -n "$namespace" exec "${prefix}-ether-$role-$ordinal" -c ether -- \
    /usr/lib/ether/ether cluster drain --data-dir /config/ether --wait 120s
  done
 done
 kubectl -n "$namespace" scale statefulset -l "app.kubernetes.io/instance=$prefix" --replicas=0
 for ((attempt=0; attempt<240; attempt++)); do
  count=$(kubectl -n "$namespace" get pods -l "app.kubernetes.io/instance=$prefix" -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["items"]))')
  [[ $count != 0 ]] || break
  sleep 1
 done
 [[ $count == 0 ]] || { echo 'old role pods did not retire; PG retained' >&2; exit 1; }
 "$root/packaging/kubernetes/multihost/migrate-pg-tmpfs.sh" "$state"
fi
python3 - "$root/packaging/kubernetes/multihost/values.yaml" "$state/values.yaml" "$prefix" "$namespace" <<'PYVALUES'
import sys,yaml
v=yaml.safe_load(open(sys.argv[1]))
v['topologySpreadConstraints'][0]['labelSelector']['matchLabels']['app.kubernetes.io/instance']=sys.argv[3]
v['ingress']['annotations']['kubernetes.io/ingress.class']=sys.argv[4]
v['ingress']['hosts']=['ether035-roles.test' if sys.argv[3]=='ether035-mh' else sys.argv[3]+'.test']
yaml.safe_dump(v,open(sys.argv[2],'w'))
PYVALUES
helm upgrade "$prefix" "$chart" -n "$namespace" \
 -f "$state/values.yaml" "${extra[@]}" \
 --set-string "image.tag=$tag" "${post[@]}"
# OnDelete preserves healthy workers; replace only ordinals still on old image.
for role in server source transcode delivery; do
 for ordinal in 0 1; do
  pod="${prefix}-ether-$role-$ordinal"
  if kubectl -n "$namespace" get pod "$pod" >/dev/null 2>&1; then
   actual=$(kubectl -n "$namespace" get pod "$pod" -o 'jsonpath={.spec.containers[0].image}')
   if [[ $actual != "ether035-multihost:$tag" ]]; then "$root/packaging/kubernetes/rollout.sh" "$namespace" "$pod"; fi
  fi
 done
done
for ((attempt=0; attempt<180; attempt++)); do
 count=$(kubectl -n "$namespace" get pods -l "app.kubernetes.io/instance=$prefix" -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["items"]))')
 [[ $count != 8 ]] || break
 sleep 1
done
[[ $count == 8 ]] || { echo 'expected eight role pods did not appear' >&2; exit 1; }
kubectl -n "$namespace" wait --for=condition=Ready pod -l "app.kubernetes.io/instance=$prefix" --timeout=300s
kubectl -n "$namespace" get pods -o json > "$state/pods-after-gated-roll.json"
echo 'gated image ready; verify exact image IDs, legacy retirement and free capacity before playback'
