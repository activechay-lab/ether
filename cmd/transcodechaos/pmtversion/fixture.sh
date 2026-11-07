#!/bin/sh
# Usage: fixture.sh OUTPUT_DIRECTORY [FFMPEG]
set -eu
out=$1
ff=${2:-ffmpeg}
mkdir -p "$out"
for spec in 'aac 0' 'ac3 8'; do
  set -- $spec
  "$ff" -hide_banner -loglevel error -y -f lavfi -i 'testsrc2=size=320x180:rate=24' -f lavfi -i 'sine=sample_rate=48000' -t 8 -c:v libx264 -preset ultrafast -threads 2 -g 96 -sc_threshold 0 -c:a "$1" -streamid 0:256 -streamid 1:257 -output_ts_offset "$2" -f mpegts "$out/$1.ts"
done
go run -p 2 ./cmd/transcodechaos/pmtversion "$out/ac3.ts" "$out/ac3-bumped.ts"
cat "$out/aac.ts" "$out/ac3.ts" > "$out/switch-unbumped.ts"
cat "$out/aac.ts" "$out/ac3-bumped.ts" > "$out/switch-bumped.ts"
