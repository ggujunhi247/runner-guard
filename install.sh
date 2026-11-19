#!/bin/sh
# Install, reinstall or upgrade runner-guard from GitHub Releases (Linux, root).
#   curl -fsSL https://raw.githubusercontent.com/ggujunhi247/runner-guard/main/install.sh | sudo sh
# Pin a release:  ... | sudo RG_VERSION=v0.6.0 sh
# The existing config, state and history are kept.
set -eu
repo=ggujunhi247/runner-guard
ver=${RG_VERSION:-latest}
asset=runner-guard_linux_amd64

[ "$(id -u)" = 0 ] || { echo "runner-guard install: run as root (sudo)" >&2; exit 1; }
case "$(uname -m)" in
x86_64 | amd64) ;;
*) echo "runner-guard install: only linux/amd64 is released" >&2; exit 1 ;;
esac
if [ "$ver" = latest ]; then
	base="https://github.com/$repo/releases/latest/download"
else
	base="https://github.com/$repo/releases/download/$ver"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
(cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c -)
chmod +x "$tmp/$asset"
"$tmp/$asset" selftest
"$tmp/$asset" install
echo "runner-guard $(/usr/local/bin/runner-guard version) installed; check: sudo runner-guard status"
