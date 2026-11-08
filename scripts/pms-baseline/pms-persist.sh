#!/bin/sh
# Moves a scanned PMS (pms-up.sh) from its tmpfs volume to a disk volume
# and restarts it there, for measurement.
#
#   pms-persist.sh NAME MEDIA_DIR
#
# A tmpfs volume is emptied when its last container stops, so the copy is
# made while the container still runs, with only the PMS service stopped
# inside it (its database closed).
set -eu
name=$1 media=$2
here=$(cd "$(dirname "$0")" && pwd)
docker exec "$name" s6-svc -wD -d /var/run/s6/services/plex
docker volume create "$name-config" >/dev/null
docker run --rm --name "$name-copy" --network none -v "$name-scan:/from:ro" -v "$name-config:/to" \
  --entrypoint sh plexinc/pms-docker:1.43.5.11079-b3b13e506 -c 'cp -a /from/. /to/ && sync && du -sh /to'
docker rm -f "$name" >/dev/null
docker volume rm "$name-scan" >/dev/null
CONFIG_VOLUME="$name-config" "$here/pms-up.sh" "$name" "$media"
