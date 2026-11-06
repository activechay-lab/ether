#!/usr/bin/env bash
# Remove only lab containers captured by provisioning. Retain bound data/secrets.
set -euo pipefail
state=${1:?usage: teardown.sh PRIVATE_STATE}
for node in relay agent2 agent1 server; do
 file="$state/$node/node.id"
 [[ $node != relay ]] || file="$state/relay.id"
 [[ -f $file ]] || continue
 cid=$(cat "$file")
 [[ $cid =~ ^[a-f0-9]{64}$ ]] || exit 64
 if ! docker inspect "$cid" >/dev/null 2>&1; then continue; fi
 [[ $(docker inspect --format '{{index .Config.Labels "ether.lab"}}' "$cid") == ether035-multihost ]] || exit 64
 if [[ $(docker inspect --format '{{.State.Paused}}' "$cid") == true ]]; then docker unpause "$cid" >/dev/null; fi
 docker stop --timeout 10 "$cid" >/dev/null
 docker rm --volumes "$cid"
done
if [[ -f $state/server/network.id ]]; then
 network=$(cat "$state/server/network.id")
 [[ $(docker network inspect --format '{{index .Labels "ether.lab"}}' "$network") == ether035-multihost ]] || exit 64
 docker network rm "$network"
fi
echo 'Bound data, kubeconfig and credentials retained in private state; no host services changed.'
