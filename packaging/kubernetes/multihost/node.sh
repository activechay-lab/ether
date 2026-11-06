#!/usr/bin/env bash
# Run locally on each physical host; no host network namespace is used.
set -euo pipefail
umask 077
mode=${1:?usage: node.sh server|agent STATE_DIR EXTERNAL_IP [SERVER_IP]}
state=${2:?private state directory required}
source "$(dirname "$0")/lab-config.sh"
external=${3:?Tailscale IP required}
server=${4:-$external}
[[ $mode == server || $mode == agent ]] || exit 64
[[ $external =~ ^100\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 64
mkdir -p "$state"
chmod 700 "$state"
[[ ! -e $state/node.id ]] || { echo 'node.id already exists; restore that exact container instead' >&2; exit 1; }
[[ -s $state/token ]] || { echo 'provision private token file first' >&2; exit 1; }
printf 'K3S_TOKEN=%s\n' "$(cat "$state/token")" > "$state/node.env"
name=$prefix-$mode
node_cpus=2
node_memory=$server_memory
if [[ $mode == agent ]]; then node_cpus=1; node_memory=$agent_memory; fi
# WireGuard overhead fits inside the 1280-byte Tailscale interface.
# Lower bridge MTU also makes Flannel's interface-derived MTU conservative.
if [[ ! -s $state/network.id ]]; then
 docker network create --label ether.lab="$lab" \
  --opt com.docker.network.driver.mtu=1280 "$name" > "$state/network.id"
fi
args=(--name "$name" --hostname "$name" --label ether.lab="$lab"
  --privileged --cgroupns=private --network "$(cat "$state/network.id")"
  --memory="${node_memory}" --memory-swap="${node_memory}" --cpus="$node_cpus" --pids-limit=4096
  --tmpfs /run --tmpfs /var/run --env-file "$state/node.env"
  -v "$state/data:/var/lib/rancher/k3s" -v "$state/kubelet:/var/lib/kubelet"
  -p "$external:$((18035 + port_offset)):30080/tcp")
if [[ $backend == wireguard-native ]]; then args+=(-p "$external:$((51820 + port_offset)):51820/udp"); fi
node=("$mode" --node-external-ip "$external" --node-label "ether.lab/host=$mode"
  --kubelet-arg max-pods=32 --kubelet-arg eviction-hard=memory.available\<100Mi,nodefs.available\<5%)
if [[ $mode == server ]]; then
  args+=(-p "$external:$((16443 + port_offset)):6443/tcp")
  node+=(--advertise-port "$((16443 + port_offset))" --tls-san "$external" --disable traefik --disable servicelb --disable metrics-server
    --cluster-cidr "$pod_cidr" --service-cidr "$service_cidr"
    --flannel-backend "$backend"
    --kube-apiserver-arg kubelet-preferred-address-types=ExternalIP,InternalIP)
  if [[ $backend == wireguard-native ]]; then node+=(--flannel-external-ip); fi
else
  node+=(--server "https://$server:$((16443 + port_offset))" --node-label ether.lab/delivery=true)
fi
docker run -d "${args[@]}" rancher/k3s:v1.34.1-k3s1 "${node[@]}" > "$state/node.id"
cat "$state/node.id"
