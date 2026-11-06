#!/usr/bin/env bash
# Import an exact image into owned local nodes, without a daemon registry change.
set -euo pipefail
state=${1:?usage: import.sh STATE_DIR IMAGE}
image=${2:?immutable image required}
archive="$state/image.tar"
trap 'test ! -f "$archive" || unlink "$archive"' EXIT
docker save "$image" -o "$archive"
for node in server agent1 agent2; do
 cid=$(cat "$state/$node/node.id")
 [[ $(docker inspect --format '{{index .Config.Labels "ether.lab"}}' "$cid") == ether035-multihost ]] || exit 64
 docker exec -i "$cid" ctr -n k8s.io images import - < "$archive"
done
