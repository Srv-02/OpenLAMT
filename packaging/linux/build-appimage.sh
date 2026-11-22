#!/usr/bin/env bash
# Builds OpenLAMT-<tag>-linux-x64.AppImage from a ready-built Linux binary.
# Usage: packaging/linux/build-appimage.sh <binary> <output file>
# Needs appimagetool (x86_64) in the PATH or in $APPIMAGETOOL. Build the binary on an old
# distribution (the release workflow uses ubuntu-22.04): an AppImage runs only on systems with
# at least the glibc of the system it was built on.
set -euo pipefail

bin="${1:?binary missing}"
out="${2:?output file missing}"
here="$(cd "$(dirname "$0")" && pwd)"
tool="${APPIMAGETOOL:-appimagetool}"

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
app="$root/OpenLAMT.AppDir"

install -Dm755 "$bin" "$app/usr/bin/openlamt"
install -Dm644 "$here/openlamt.desktop" "$app/openlamt.desktop"
install -Dm644 "$here/openlamt.png" "$app/openlamt.png"
install -Dm644 "$here/openlamt.png" "$app/usr/share/icons/hicolor/512x512/apps/openlamt.png"
install -Dm644 "$here/../../LICENSE" "$app/usr/share/doc/openlamt/copyright"
install -Dm644 "$here/../../NOTICE" "$app/usr/share/doc/openlamt/NOTICE"
ln -s openlamt.png "$app/.DirIcon"
# The binary needs no environment set up, so AppRun is a link to it.
ln -s usr/bin/openlamt "$app/AppRun"

mkdir -p "$(dirname "$out")"
# --appimage-extract-and-run: CI runners have no FUSE. No update information is embedded.
ARCH=x86_64 "$tool" --appimage-extract-and-run --no-appstream "$app" "$out"
chmod 755 "$out"
