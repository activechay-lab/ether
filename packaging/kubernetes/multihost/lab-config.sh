#!/usr/bin/env bash
# Shared defaults preserve the original lab. A private lab.env selects another.
if [[ -f $state/lab.env ]]; then source "$state/lab.env";
elif [[ -f $state/../lab.env ]]; then source "$state/../lab.env"; fi
lab=${ETHER_LAB_NAME:-ether035-multihost}
prefix=${ETHER_LAB_PREFIX:-ether035-mh}
namespace=${ETHER_LAB_NAMESPACE:-ether035-ha}
context=${ETHER_LAB_CONTEXT:-$lab}
port_offset=${ETHER_LAB_PORT_OFFSET:-0}
server_memory=${ETHER_LAB_SERVER_MEMORY:-2048m}
agent_memory=${ETHER_LAB_AGENT_MEMORY:-2048m}
pod_cidr=${ETHER_LAB_POD_CIDR:-10.65.0.0/16}
service_cidr=${ETHER_LAB_SERVICE_CIDR:-10.66.0.0/16}
backend=${ETHER_LAB_BACKEND:-wireguard-native}
[[ $lab =~ ^ether035-[a-z0-9-]+$ && $prefix =~ ^ether035-[a-z0-9-]+$ && $namespace =~ ^ether035-[a-z0-9-]+$ ]] || exit 64
[[ $context == "$lab" && $port_offset =~ ^[0-9]+$ ]] || exit 64
