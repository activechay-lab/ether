#!/usr/bin/env bash
# Run on the second host only AFTER the documented continuous PSI/clock gate.
set -euo pipefail
state=${1:?usage: join.sh PRIVATE_STATE TAILSCALE_IP SERVER_TAILSCALE_IP}
external=${2:?Tailscale IP required}; server=${3:?server Tailscale IP required}
root=$(cd "$(dirname "$0")" && pwd)
if [[ -s $state/node.id ]]; then
 cid=$(cat "$state/node.id")
 [[ $cid =~ ^[a-f0-9]{64}$ ]] || exit 64
 read -r label network running <<< "$(docker inspect --format '{{index .Config.Labels "ether.lab"}} {{.HostConfig.NetworkMode}} {{.State.Running}}' "$cid")"
 [[ $label == ether035-multihost && $network != host && $running == false ]] || exit 64
 docker update --memory=2g --memory-swap=2g --cpus=1 "$cid" >/dev/null
 docker start "$cid"
else
 "$root/node.sh" agent "$state" "$external" "$server"
fi
