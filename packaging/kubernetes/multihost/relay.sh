#!/usr/bin/env bash
set -euo pipefail
state=${1:?usage: relay.sh PRIVATE_STATE TAILSCALE_IP}
host=${2:?Tailscale IP required}
[[ $host =~ ^100\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 64
root=$(cd "$(dirname "$0")/../../.." && pwd)
[[ ! -e $state/relay.id ]] || exit 64
args=()
for port in 15432 18410 18411 18420 18421 18430 18431 18440 18441; do
 args+=(-p "$host:$port:$port")
done
docker run -d --name ether035-mh-relay --label ether.lab=ether035-multihost \
 --network "$(cat "$state/server/network.id")" --memory=64m --memory-swap=64m --cpus=0.25 \
 --pids-limit=128 --read-only --cap-drop=ALL --security-opt=no-new-privileges --user 65534:65534 \
 "${args[@]}" -v "$root/packaging/kubernetes/multihost/relay.py:/relay.py:ro" \
 python:3.12.12-slim-bookworm python3 -B /relay.py > "$state/relay.id"
