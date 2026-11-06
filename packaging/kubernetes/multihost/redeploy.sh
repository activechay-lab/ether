#!/usr/bin/env bash
# Build one reviewed commit, import it, then update templates. Does not delete pods.
set -euo pipefail
umask 077
state=${1:?usage: redeploy.sh PRIVATE_STATE INTEGRATION_SHA}
revision=${2:?reviewed integration SHA required}
root=$(cd "$(dirname "$0")/../../.." && pwd)
sha=$(git -C "$root" rev-parse --verify "$revision^{commit}")
tag=${sha:0:10}
source_dir=$(mktemp -d "$state/build.XXXXXX")
cleanup() { python3 - "$source_dir" <<'PY'
import shutil,sys
shutil.rmtree(sys.argv[1])
PY
}
trap cleanup EXIT
git -C "$root" archive "$sha" | tar -x -C "$source_dir"
(cd "$source_dir" && nice -n 10 env CGO_ENABLED=0 GOMAXPROCS=4 "${GO_BINARY:-/data/bb/toolchains/go1.27.3/bin/go}" build -p 2 -trimpath -ldflags '-s -w' -o "$state/image/ether" ./cmd/ether)
mkdir -p "$state/image/packaging/docker"
(cd "$source_dir" && FFMPEG_OUT="$state/image/dist/ffmpeg" nice -n 10 scripts/fetch-ffmpeg.sh linux-amd64)
cp "$source_dir/LICENSE" "$source_dir/THIRD_PARTY_NOTICES.md" "$state/image/"
cp "$source_dir/packaging/docker/entrypoint.sh" "$state/image/packaging/docker/"
(cd "$source_dir" && nice -n 10 docker build --label "org.opencontainers.image.revision=$sha" \
 -t "ether035-multihost:$tag" -f packaging/docker/Dockerfile.goreleaser.amd64 "$state/image")
"$root/packaging/kubernetes/multihost/import.sh" "$state" "ether035-multihost:$tag"
"$root/packaging/kubernetes/multihost/install.sh" "$state" "$tag"
echo 'Templates updated. StatefulSets are OnDelete; drain and replace each pod with rollout.sh.'
