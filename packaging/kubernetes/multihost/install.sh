#!/usr/bin/env bash
# Install fixtures and chart into the explicitly isolated multihost context.
set -euo pipefail
umask 077
state=${1:?usage: install.sh PRIVATE_STATE IMAGE_TAG}
source "$(dirname "$0")/lab-config.sh"
case "$lab:$context:$prefix:$namespace" in
 ether035-multihost:ether035-multihost:ether035-mh:ether035-ha|ether035-lab2:ether035-lab2:ether035-lab2:ether035-lab2) ;;
 *) echo 'only approved isolated lab tuples are allowed' >&2; exit 64;;
esac
export ETHER_LAB_PREFIX="$prefix" ETHER_LAB_NAMESPACE="$namespace"
tag=${2:?immutable image tag required}
root=$(cd "$(dirname "$0")/../../.." && pwd)
export KUBECONFIG="$state/kubeconfig"
export PATH="$state/bin:$PATH"
[[ $(kubectl config current-context) == "$context" ]] || exit 64
kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
if [[ ${ETHER_LAB_SINGLE_NODE:-false} == true ]]; then
 kubectl label node "$prefix-server" ether.lab/workload=true --overwrite
else
 kubectl label node "$prefix-agent1" "$prefix-agent2" ether.lab/workload=true --overwrite
fi
mkdir -p "$state/private"
chmod 700 "$state/private"
if [[ ! -f $state/private/database-url ]]; then
  python3 - "$state/private" "$namespace" <<'PY'
import pathlib,secrets,sys
p=pathlib.Path(sys.argv[1]); password=secrets.token_urlsafe(24)
(p/'password').write_text(password)
(p/'database-url').write_text('postgres://ether:'+password+'@'+sys.argv[2]+'-pg.'+sys.argv[2]+'.svc.cluster.local:5432/ether?sslmode=disable')
(p/'cluster-key').write_bytes(secrets.token_bytes(32))
PY
  openssl genpkey -algorithm ED25519 -out "$state/private/ca.key"
  openssl req -new -x509 -key "$state/private/ca.key" -out "$state/private/ca.crt" -days 30 \
    -subj /CN=ether035-multihost-lab -addext basicConstraints=critical,CA:TRUE \
    -addext keyUsage=critical,keyCertSign,cRLSign
  cp "$state/private/ca.crt" "$state/private/roots.pem"
fi
for pair in 'ether-db database-url' 'ether-cluster cluster-key' "${namespace}-pg password"; do
  read -r name key <<< "$pair"
  if ! kubectl -n "$namespace" get secret "$name" >/dev/null 2>&1; then
    kubectl -n "$namespace" create secret generic "$name" --from-file="$key=$state/private/$key"
  fi
done
if ! kubectl -n "$namespace" get secret ether-authority >/dev/null 2>&1; then
 kubectl -n "$namespace" create secret generic ether-authority \
  --from-file="ca.key=$state/private/ca.key" --from-file="ca.crt=$state/private/ca.crt" --from-file="roots.pem=$state/private/roots.pem"
fi
fixture_flags=()
# Reapplying a changed emptyDir template could discard the existing DB.
if kubectl -n "$namespace" get statefulset "${namespace}-pg" >/dev/null 2>&1; then
 fixture_flags+=(--preserve-pg)
fi
if kubectl -n "$namespace" get deployment "${namespace}-source" >/dev/null 2>&1; then
 fixture_flags+=(--preserve-source)
else
 kubectl -n "$namespace" create configmap "${namespace}-source-code" --from-file="$root/packaging/kubernetes/source-fixture.py" --dry-run=client -o yaml | kubectl -n "$namespace" apply -f -
fi
python3 "$root/packaging/kubernetes/multihost/fixtures.py" "$root" "$tag" "${fixture_flags[@]}" > "$state/fixtures.yaml"
kubectl -n "$namespace" apply -f "$state/fixtures.yaml"
kubectl -n "$namespace" rollout status "statefulset/${namespace}-pg" --timeout=180s
postrender=()
if [[ -s $state/external-private-ip ]]; then
 export MULTIHOST_IP=$(cat "$state/external-private-ip")
 postrender=(--post-renderer "$root/packaging/kubernetes/multihost/external-private.py")
fi
extra_values=()
if [[ -s $state/physical-host.values.yaml ]]; then
 extra_values=(-f "$state/physical-host.values.yaml")
fi
python3 - "$root/packaging/kubernetes/multihost/values.yaml" "$state/values.yaml" "$prefix" "$namespace" <<'PYVALUES'
import sys,yaml
v=yaml.safe_load(open(sys.argv[1]))
v['topologySpreadConstraints'][0]['labelSelector']['matchLabels']['app.kubernetes.io/instance']=sys.argv[3]
v['ingress']['annotations']['kubernetes.io/ingress.class']=sys.argv[4]
v['ingress']['hosts']=['ether035-roles.test' if sys.argv[3]=='ether035-mh' else sys.argv[3]+'.test']
yaml.safe_dump(v,open(sys.argv[2],'w'))
PYVALUES
helm upgrade --install "$prefix" "$root/packaging/helm/ether" -n "$namespace" \
 -f "$state/values.yaml" "${extra_values[@]}" --set-string "image.tag=$tag" "${postrender[@]}" --wait --timeout 10m
