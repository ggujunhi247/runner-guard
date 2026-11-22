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
curl -fsSL -o "$tmp/checksums.txt.sig" "$base/checksums.txt.sig"
# The release keys (private halves kept off GitHub); same as internal/update.PublicKeys.
cat >"$tmp/key1.pem" <<'EOF'
-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEA4xuHI4gBNCLStJKC0Fc2
FdwhBJleuR7p9awiT8d8TXJO1zZaWowAhz8nLIwq02NBRXdC8X6MMeBgqA194Llb
poZk0AulasLIfSLpCq9GfPEjUUfIlwVOoeouROVl2VQJPZs7Sy/iffFaux5uOPJG
9cLj5XqHiy5q6UnJey7kwyOl7Bi/rblULz95dF37WTmh7Gxikkh5cmXUPig6D82c
T02YoEvKOLUK6m94KqA1HJ+wLpVrjPQA52u8dyAtr6Dl+ajlnkZH7Pq1tQB4C5RC
h1NJz75mpBupKgnTqzmrzFEcUZZC3K6pVejeZnBYZRY7j8UCzSqZgyNLBIY+9hLo
/wIqG4XYPDJ/bVS2vcRAex6YmbnxA9Ur3/14ZBayfEliuIkegDOgm0cza7RuqHK2
NxtdN4ChKEFWJgjm+Sw7ZMAUDPkTUrmQ29Pyd5HFmwRfWgF9RjEbmD6A5Jf5sgOO
cZuuc5mziBKQAeDhjaT5nShJ9VkxdIep5m3vx8Cs5bn5AgMBAAE=
-----END PUBLIC KEY-----
EOF
cat >"$tmp/key2.pem" <<'EOF'
-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEA2/gLMUkbQ3Ieml5ASxxD
ILvXKhmAsaQXc78u4fUfIkNE8xdCOZuRJLh7nR3rZU/rWDuptHy6rwORbDbYNZ4b
pNNbx0EFMLij4iAoGHzt2WZSre4lrQTq9+sOyUJgsDR33TW2eVFQb/6yQGqyKqVq
kivf+uJKge291pf+o03W3J7ZK243OQyr+biNmYs/Y/jecE6FxELRVWc203rEXdfX
8BN9l1+zEMKpTXI9jy+/e5mFpHgGbg7QBrTpcBisUj41MUVl6Jl7KbyrATJ586pq
d8l4LiiicV9rqxNqFJRN6jGRJUhaD7DxUw6Cyrhsx69Rxr04DTurWDGgkuo1iHzV
x/d7lvVUgV+/Go6H9aBZNkbiUKoSaxy9a1YTHtjWw9JJBr13mg3ILm8sWQC5jn+O
4ggstjcYV1t0yv5gDBbQWPdDLahS2USq9I9Bu9AbaPdOCkuGC/H+mCWdC1hKVIAW
dJCnFgKls3ZOHtUeaS7WGR2vEVDyjDF3lROcdiKboIrBAgMBAAE=
-----END PUBLIC KEY-----
EOF
command -v openssl >/dev/null || { echo "runner-guard install: openssl is needed to check the release signature" >&2; exit 1; }
signed=no
for k in "$tmp/key1.pem" "$tmp/key2.pem"; do
	if openssl dgst -sha256 -verify "$k" -signature "$tmp/checksums.txt.sig" "$tmp/checksums.txt" >/dev/null 2>&1; then signed=yes; fi
done
[ "$signed" = yes ] || { echo "runner-guard install: release signature does not match; not installing" >&2; exit 1; }
(cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c -)
chmod +x "$tmp/$asset"
"$tmp/$asset" selftest
"$tmp/$asset" install
echo "runner-guard $(/usr/local/bin/runner-guard version) installed; check: sudo runner-guard status"
