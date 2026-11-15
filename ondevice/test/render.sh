#!/bin/sh
# Renders the UI's screens to PNG files with a mock backend. Needs Qt 6's qml tool.
# usage: ondevice/test/render.sh <output directory>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: render.sh <output directory>}
mkdir -p "$out"
QML=${QML:-/usr/lib/qt6/bin/qml}
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software \
    "$QML" -I "$here/mock" "$here/render.qml" -- "$(cd "$out" && pwd)"
