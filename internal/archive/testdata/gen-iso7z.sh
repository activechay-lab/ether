#!/usr/bin/env bash
# gen-iso7z.sh regenerates the encrypted disc image fixtures in
# testdata/iso-crypto: a small Blu-ray image (isotest/cmd/genbluray) stored
# in a 7z with AES-256 content encryption. The outputs are committed; tests
# never run this script. Requires 7zz (7-Zip 23+) and go.
#
#   bash internal/archive/testdata/gen-iso7z.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
out="$here/iso-crypto"
mkdir -p "$out"
rm -f "$out"/*.7z
(cd "$root" && go run ./internal/iso/isotest/cmd/genbluray "$out")
cd "$out"
7zz a -t7z -m0=Copy -mhe=off -pSecret disc.7z disc.iso >/dev/null
rm -f disc.iso
echo "wrote $out"
