#!/usr/bin/env bash
# Reversible fault around an explicit verifier command. Always restores node faults.
set -euo pipefail
if (( $# < 4 )); then
 echo 'usage: loss.sh stop|pause|partition STATE_DIR PEER_TAILSCALE_IP VERIFIER [ARGS...]' >&2; exit 64
fi
fault=$1; state=$2; peer=$3; shift 3
[[ $fault == stop || $fault == pause || $fault == partition ]] || exit 64
[[ $peer =~ ^100\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 64
cid=$(cat "$state/node.id")
[[ $cid =~ ^[a-f0-9]{64}$ ]] || exit 64
# Inspect captured immutable ID, lab label AND private network namespace.
read -r label network running <<< "$(docker inspect --format '{{index .Config.Labels "ether.lab"}} {{.HostConfig.NetworkMode}} {{.State.Running}}' "$cid")"
[[ $label == ether035-multihost && $network != host && $running == true ]] || exit 64
active=false
input_rule=false
output_rule=false
restore() {
 $active || return 0
 case $fault in
  stop) docker start "$cid" >/dev/null ;;
  pause) docker unpause "$cid" >/dev/null ;;
  partition)
   if $input_rule; then docker exec "$cid" iptables -D INPUT -s "$peer" -m comment --comment ether035-mh-loss -j DROP; fi
   if $output_rule; then docker exec "$cid" iptables -D OUTPUT -d "$peer" -m comment --comment ether035-mh-loss -j DROP; fi ;;
 esac
}
trap restore EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
active=true
case $fault in
 stop)
  docker stop --timeout 0 "$cid" >/dev/null
  [[ $(docker inspect --format '{{.State.Running}} {{.State.Pid}}' "$cid") == 'false 0' ]] ;;
 pause) docker pause "$cid" >/dev/null ;;
 partition)
  # Only the node container's network namespace. Never host iptables.
  docker exec "$cid" iptables -I INPUT -s "$peer" -m comment --comment ether035-mh-loss -j DROP
  input_rule=true
  docker exec "$cid" iptables -I OUTPUT -d "$peer" -m comment --comment ether035-mh-loss -j DROP
  output_rule=true ;;
esac
printf 'fault=%s container=%s utc=%s\n' "$fault" "$cid" "$(date -u +%FT%TZ)"
"$@"
