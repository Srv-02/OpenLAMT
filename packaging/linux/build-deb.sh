#!/usr/bin/env bash
# Builds OpenLAMT_<version>_amd64.deb from a ready-built Linux binary.
# Usage: packaging/linux/build-deb.sh <binary> <version> <output file>
# <version> is the display version (0.1.0 or 0.1.0-rc14); Debian sorts pre-releases before
# the release with "~", so this becomes 0.1.0~rc14.
set -euo pipefail

bin="${1:?Binary fehlt}"
version="${2:?Version fehlt}"
out="${3:?Ausgabedatei fehlt}"
here="$(cd "$(dirname "$0")" && pwd)"
debversion="${version/-/\~}"

root="$(mktemp -d)"
chmod 755 "$root"
trap 'rm -rf "$root"' EXIT

install -Dm755 "$bin" "$root/usr/bin/openlamt"
install -Dm644 "$here/openlamt.desktop" "$root/usr/share/applications/openlamt.desktop"
install -Dm644 "$here/openlamt.png" "$root/usr/share/icons/hicolor/512x512/apps/openlamt.png"
install -Dm644 "$here/../../LICENSE" "$root/usr/share/doc/openlamt/copyright"
install -Dm644 "$here/../../NOTICE" "$root/usr/share/doc/openlamt/NOTICE"

size="$(du -sk "$root" | cut -f1)"
mkdir -p "$root/DEBIAN"
# Depends: GLFW/OpenGL runtime libraries (X11 and Wayland; GLFW loads some Wayland libraries via dlopen). Recommends: a
# Secret Service provider for the password keychain (go-keyring talks D-Bus Secret Service).
cat > "$root/DEBIAN/control" <<CONTROL
Package: openlamt
Version: ${debversion}
Section: net
Priority: optional
Architecture: amd64
Installed-Size: ${size}
Maintainer: Michael Mayer <8415653+Srv-02@users.noreply.github.com>
Depends: libc6, libgl1, libx11-6, libxcursor1, libxrandr2, libxinerama1, libxi6, libxxf86vm1, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0
Recommends: gnome-keyring | keepassxc
Homepage: https://github.com/Srv-02/openlamt
Description: Native desktop client for Intel AMT
 OpenLAMT offers power control, KVM and favorites for Intel AMT devices.
 Passwords are stored in the Secret Service keychain (for example
 GNOME Keyring or KeePassXC); without one the app cannot save passwords.
CONTROL

mkdir -p "$(dirname "$out")"
dpkg-deb --root-owner-group -Zxz --build "$root" "$out"
