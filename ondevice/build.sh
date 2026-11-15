#!/bin/sh
# Builds the AppLoad app folder for the Paper Pro Move into ondevice/output/remanager.
# Needs Go and Qt 6's rcc. Copy the result to /home/root/xovi/exthome/appload/ on the tablet.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out="$here/output/remanager"
RCC=${RCC:-$(command -v rcc || echo /usr/lib/qt6/libexec/rcc)}

rm -rf "$out"
mkdir -p "$out/backend"
cp "$here/manifest.json" "$here/icon.png" "$out/"
# icon.png is rendered from icon.svg, the desktop icon without its background and with black strokes.
# Uncompressed, so the file does not depend on which compressors the tablet's Qt was built with.
(cd "$here" && "$RCC" --binary --no-compress -o "$out/resources.rcc" application.qrc)
(cd "$here/.." && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w" -o "$out/backend/entry" ./cmd/remanager-ondevice)
echo "built $out"
