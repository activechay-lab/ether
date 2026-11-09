#!/bin/sh
# Writes each movie in OUT_DIR/movies.tsv (from makelib.py) as its own MP4
# with embedded tags, remuxing the sample from BASE_DIR (no re-encode).
#
#   tag-movies.sh BASE_DIR OUT_DIR
#
# Runs ffmpeg from the pinned linuxserver/ffmpeg image; the host has none.
set -eu
base=$(cd "$1" && pwd) out=$(cd "$2" && pwd)
docker run --rm --name ether-036-pms-ffmpeg-gen --user "$(id -u):$(id -g)" \
  -v "$base:/base:ro" -v "$out:/out" --entrypoint sh linuxserver/ffmpeg:9.0.2-cli-ls89 -c '
tab=$(printf "\t")
while IFS="$tab" read -r variant path title year genres summary; do
  [ -s "/out/$path" ] && continue
  ffmpeg -nostdin -hide_banner -loglevel error -y -i "/base/$variant" -map 0 -c copy \
    -metadata title="$title" -metadata date="$year" -metadata genre="$genres" \
    -metadata synopsis="$summary" -metadata description="$summary" "/out/$path.tmp.mp4" &&
    mv "/out/$path.tmp.mp4" "/out/$path"
done < /out/movies.tsv'
