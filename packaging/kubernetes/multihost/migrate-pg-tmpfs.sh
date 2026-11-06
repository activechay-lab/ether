#!/usr/bin/env bash
# LAB ONLY: preserve a consistent DB dump, retain the disk PVC, move PG to tmpfs.
# Run only with playback stopped and under the shared heavy-I/O lock.
set -euo pipefail
umask 077
state=${1:?usage: migrate-pg-tmpfs.sh PRIVATE_STATE}
[[ ${ETHER_HEAVY_IO_LOCKED:-0} == 1 ]] || { echo 'invoke through heavy-io.sh' >&2; exit 64; }
source "$(dirname "$0")/lab-config.sh"
case "$lab:$context:$prefix:$namespace" in
 ether035-multihost:ether035-multihost:ether035-mh:ether035-ha|ether035-lab2:ether035-lab2:ether035-lab2:ether035-lab2) ;;
 *) echo 'only approved isolated lab tuples are allowed' >&2; exit 64;;
esac
export ETHER_LAB_PREFIX="$prefix" ETHER_LAB_NAMESPACE="$namespace"
root=$(cd "$(dirname "$0")/../../.." && pwd)
export KUBECONFIG="$state/kubeconfig" PATH="$state/bin:$PATH"
[[ $(kubectl config current-context) == "$context" ]] || exit 64
[[ ! -e /data/bb/thread-storage/batch-gate.active ]] || { echo 'batch gate has priority' >&2; exit 75; }
kubectl -n "$namespace" get statefulset ${namespace}-pg -o json > "$state/pg-before-tmpfs.json"
# Refuse a repeat migration: a tmpfs pod replacement would destroy its live DB.
python3 - "$state/pg-before-tmpfs.json" "$namespace" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
assert s['metadata']['name']==sys.argv[2]+'-pg'
assert not any(v.get('emptyDir',{}).get('medium')=='Memory' for v in s['spec']['template']['spec'].get('volumes',[])), 'already tmpfs: retain current pod'
PY
# The caller drains/stops the eight role pods first, so the snapshot cannot
# roll back a concurrently issued grant or generation floor.
roles=$(kubectl -n "$namespace" get pods -l "app.kubernetes.io/instance=$prefix" -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["items"]))')
[[ $roles == 0 ]] || { echo 'drain and stop all Ether roles before DB migration' >&2; exit 64; }
mkdir -p "$state/private"
chmod 700 "$state/private"
kubectl -n "$namespace" get service ${namespace}-pg -o json > "$state/pg-service-before-tmpfs.json"
# Block fresh TCP clients and close any leftover client transaction before dump;
# all migration commands use the pod's local Unix socket.
kubectl -n "$namespace" patch service ${namespace}-pg --type merge -p "{\"spec\":{\"selector\":{\"app\":\"${namespace}-pg-restore-hold\"}}}"
kubectl -n "$namespace" exec ${namespace}-pg-0 -- psql -U ether -d ether -Atq -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND client_addr IS NOT NULL AND pid<>pg_backend_pid();" >/dev/null
backup="$state/private/pg-before-tmpfs-$(date -u +%Y%m%dT%H%M%SZ).dump"
kubectl -n "$namespace" exec ${namespace}-pg-0 -- pg_dump -U ether -d ether -Fc --no-owner > "$backup"
[[ -s $backup ]]
printf '%s\n' "$backup" > "$state/private/pg-tmpfs-backup-path"
sha256sum "$backup" > "$backup.sha256"
# Render only PG: do not touch the original synthetic-source deployment.
python3 "$root/packaging/kubernetes/multihost/fixtures.py" "$root" unused > "$state/tmpfs-fixtures.rendered.yaml"
python3 - "$state/tmpfs-fixtures.rendered.yaml" "$state/pg-tmpfs.yaml" "$namespace" <<'PY'
import yaml,sys
objects=[x for x in yaml.safe_load_all(open(sys.argv[1])) if x.get('metadata',{}).get('name')==sys.argv[3]+'-pg' and x['kind']=='StatefulSet']
assert len(objects)==1
yaml.safe_dump_all(objects,open(sys.argv[2],'w'))
PY
# StatefulSet claim templates are immutable. Remove only its controller, retain
# the old PVC and stop its old pod normally before creating the tmpfs controller.
uid=$(kubectl -n "$namespace" get pod ${namespace}-pg-0 -o 'jsonpath={.metadata.uid}')
kubectl -n "$namespace" delete statefulset ${namespace}-pg --cascade=orphan --wait=true
printf '{"apiVersion":"v1","kind":"DeleteOptions","preconditions":{"uid":"%s"}}' "$uid" |
 kubectl delete --raw "/api/v1/namespaces/$namespace/pods/${namespace}-pg-0" -f - >/dev/null
kubectl -n "$namespace" wait --for=delete pod/${namespace}-pg-0 --timeout=120s
kubectl -n "$namespace" apply -f "$state/pg-tmpfs.yaml"
kubectl -n "$namespace" rollout status statefulset/${namespace}-pg --timeout=180s
# Restore every table and sequence. No ownership/claim/fencing rows are edited.
kubectl -n "$namespace" exec -i ${namespace}-pg-0 -- pg_restore -U ether -d ether --clean --if-exists --no-owner --exit-on-error < "$backup"
kubectl -n "$namespace" exec ${namespace}-pg-0 -- psql -U ether -d ether -Atq -c 'SHOW fsync; SHOW synchronous_commit; SHOW full_page_writes;' > "$state/pg-tmpfs-settings.txt"
[[ $(tr '\n' ' ' < "$state/pg-tmpfs-settings.txt") == 'off off off ' ]]
kubectl -n "$namespace" patch service ${namespace}-pg --type merge -p "{\"spec\":{\"selector\":{\"app\":\"${namespace}-pg\"}}}"
echo 'lab PG restored on tmpfs; private backup and original PVC retained'
