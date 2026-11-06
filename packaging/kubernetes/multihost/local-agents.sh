#!/usr/bin/env bash
# Add two bounded local agents to the server's isolated bridge.
set -euo pipefail
umask 077
state=${1:?usage: local-agents.sh PRIVATE_STATE}
network=$(cat "$state/server/network.id")
for ordinal in 1 2; do
 target="$state/agent$ordinal"
 mkdir -p "$target"
 [[ ! -e $target/node.id ]] || { echo "$target already provisioned" >&2; continue; }
 cp "$state/server/node.env" "$target/node.env"
 docker run -d --name "ether035-mh-agent$ordinal" --hostname "ether035-mh-agent$ordinal" \
  --label ether.lab=ether035-multihost --privileged --cgroupns=private --network "$network" \
  --memory=2g --memory-swap=2g --cpus=1 --pids-limit=4096 --tmpfs /run --tmpfs /var/run \
  --env-file "$target/node.env" -v "$target/data:/var/lib/rancher/k3s" -v "$target/kubelet:/var/lib/kubelet" \
  rancher/k3s:v1.34.1-k3s1 agent --server https://ether035-mh-server:6443 \
  --node-label ether.lab/host=server --node-label ether.lab/workload=true --node-label ether.lab/delivery=true --kubelet-arg max-pods=32 \
  --kubelet-arg 'eviction-hard=memory.available<100Mi,nodefs.available<5%' > "$target/node.id"
done
