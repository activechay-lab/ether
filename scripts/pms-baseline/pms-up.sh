#!/bin/sh
# Starts an unclaimed PMS for the baseline and creates its two libraries.
#
#   pms-up.sh NAME MEDIA_DIR      e.g. pms-up.sh ether-036-pms-1k media-1k
#
# The scan runs on a tmpfs config volume (NAME-scan, SCAN_TMPFS in size,
# default 4g): PMS fsyncs per item, and on a busy HDD that turns a 10k-item
# scan into hours. pms-persist.sh then copies the finished server to a disk
# volume (NAME-config) and restarts it from there for measurement.
#
# The server is never claimed (no PLEX_CLAIM) and sits on the --internal
# Docker network ether-036-pms-net, which has no route out. allowedNetworks
# trusts only the bridge gateway (this host), so the replay needs no token.
# The plex.tv names PMS calls resolve to loopback, so its offline lookups
# (codecs, metadata agents) fail at once instead of timing out per file.
set -eu
name=$1 media=$(cd "$2" && pwd)
image=plexinc/pms-docker:1.43.5.11079-b3b13e506
docker network inspect ether-036-pms-net >/dev/null 2>&1 || docker network create --internal ether-036-pms-net >/dev/null
gateway=$(docker network inspect -f '{{(index .IPAM.Config 0).Gateway}}' ether-036-pms-net)
[ -n "${CONFIG_VOLUME:-}" ] ||
  docker volume create --driver local --opt type=tmpfs --opt device=tmpfs --opt o=size="${SCAN_TMPFS:-4g}" "$name-scan" >/dev/null
docker volume create "$name-transcode" >/dev/null
hosts=""
for h in plex.tv servers.plex.tv metadata.provider.plex.tv pubsub.plex.tv clients.plex.tv resources.plex.tv features.plex.tv; do
  hosts="$hosts --add-host $h:127.0.0.1"
done
# shellcheck disable=SC2086
docker run -d --name "$name" --network ether-036-pms-net --hostname "$name" --stop-timeout 60 \
  -e TZ=UTC -e ALLOWED_NETWORKS="$gateway/32" $hosts \
  -v "${CONFIG_VOLUME:-$name-scan}:/config" -v "$name-transcode:/transcode" -v "$media:/data:ro" "$image" >/dev/null
ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$name")
P=http://$ip:32400
[ -n "${CONFIG_VOLUME:-}" ] && { echo "$name at $P"; exit 0; }
until curl -s "$P/identity" | grep -q machineIdentifier && ! curl -s "$P/identity" | grep -q startState; do sleep 2; done
curl -sf -X PUT "$P/:/prefs?GenerateBIFBehavior=never&GenerateChapterThumbBehavior=never&LoudnessAnalysisBehavior=never&GenerateIntroMarkerBehavior=never&GenerateCreditsMarkerBehavior=never&GenerateAdMarkerBehavior=never&FSEventLibraryUpdatesEnabled=0&ScheduledLibraryUpdatesEnabled=0&GdmEnabled=0&DlnaEnabled=0&PublishServerOnPlexOnlineKey=0&sendCrashReports=0" >/dev/null
curl -sf -X POST "$P/library/sections?name=Movies&type=movie&agent=tv.plex.agents.movie&scanner=Plex%20Movie&language=en-US&location=%2Fdata%2Fmovies&prefs%5BrespectTags%5D=1&prefs%5BenableBIFGeneration%5D=0" >/dev/null
curl -sf -X PUT "$P/library/sections/1/prefs?respectTags=1&enableBIFGeneration=0" >/dev/null
curl -sf -X POST "$P/library/sections?name=TV%20Shows&type=show&agent=tv.plex.agents.series&scanner=Plex%20TV%20Series&language=en-US&location=%2Fdata%2Fshows&prefs%5BenableBIFGeneration%5D=0" >/dev/null
curl -sf -X PUT "$P/library/sections/2/prefs?enableBIFGeneration=0" >/dev/null
echo "$name at $P"
