#!/usr/bin/env bash
# Sign and publish a draft release made by the release workflow, after
# proving CI built it from the tag: both binaries are rebuilt here from the
# tagged commit with the Go version recorded in them, and must match byte for
# byte. Only then is checksums.txt signed with the key kept off GitHub.
#
#   scripts/sign-release.sh vX.Y.Z           check, sign, upload .sig, publish
#   scripts/sign-release.sh --check vX.Y.Z   only check (any release, e.g. an audit)
#
# Key: $RG_SIGNING_KEY, default C:\Secrets\runner-guard\release-signing.pem
set -euo pipefail
check_only=false
if [ "${1:-}" = --check ]; then check_only=true; shift; fi
tag=${1:?usage: scripts/sign-release.sh [--check] vX.Y.Z}
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "not a vX.Y.Z tag: $tag" >&2; exit 1; }
repo=ggujunhi247/runner-guard
key=${RG_SIGNING_KEY:-/c/Secrets/runner-guard/release-signing.pem}
root=$(git rev-parse --show-toplevel)
cd "$root"

draft=$(gh release view "$tag" -R "$repo" --json isDraft -q .isDraft)
if ! $check_only; then
	[ "$draft" = true ] || { echo "$tag is already published; nothing to sign" >&2; exit 1; }
	[ -f "$key" ] || { echo "signing key not found: $key" >&2; exit 1; }
fi

work=$(mktemp -d -p "${TEMP:-${TMPDIR:-/tmp}}" rgsign.XXXXXX)
wt="$root/.worktrees/sign-$tag"
cleanup() {
	git worktree remove --force "$wt" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

echo "== download $tag"
gh release download "$tag" -R "$repo" -D "$work" -p 'runner-guard_*' -p checksums.txt -p rg-windows.ps1 -p runner-guard-windows-guide.md
(cd "$work" && sha256sum --strict -c checksums.txt)

echo "== rebuild from the tag"
git fetch -q --tags origin
commit=$(git rev-parse "$tag^{commit}")
git worktree add -q --detach "$wt" "$commit"
for asset in runner-guard_linux_amd64 runner-guard_windows_amd64.exe; do
	info=$(go version -m "$work/$asset")
	gover=$(printf '%s\n' "$info" | head -1 | awk '{print $2}')
	rev=$(printf '%s\n' "$info" | sed -n 's/^[[:space:]]*build[[:space:]]*vcs.revision=//p')
	[ "$rev" = "$commit" ] || { echo "$asset was built from $rev, not $tag ($commit)" >&2; exit 1; }
	goos=${asset#runner-guard_}; goos=${goos%%_*}
	(cd "$wt" && GOTOOLCHAIN=$gover CGO_ENABLED=0 GOOS=$goos GOARCH=amd64 go build -trimpath \
		-ldflags "-s -w -X github.com/ggujunhi247/runner-guard/internal/update.Version=$tag" \
		-o "$work/local-$asset" ./cmd/runner-guard)
	a=$(sha256sum <"$work/$asset" | cut -d' ' -f1)
	b=$(sha256sum <"$work/local-$asset" | cut -d' ' -f1)
	[ "$a" = "$b" ] || { echo "$asset differs from a local build of $tag ($gover): CI output not trusted" >&2; exit 1; }
	echo "$asset: reproduced with $gover ($a)"
done

# Sign only a checksums.txt rebuilt here from what was just reproduced, never
# CI's file as is: an extra line `sha256sum -c` skips could still be read by a
# host. The helper script and guide come from the tagged commit (git show, no
# line-ending conversion).
mkdir "$work/local"
for asset in runner-guard_linux_amd64 runner-guard_windows_amd64.exe; do
	cp "$work/local-$asset" "$work/local/$asset"
done
git show "$commit:rg-windows.ps1" >"$work/local/rg-windows.ps1"
git show "$commit:docs/windows.md" >"$work/local/runner-guard-windows-guide.md"
(cd "$work/local" && sha256sum runner-guard_linux_amd64 runner-guard_windows_amd64.exe rg-windows.ps1 runner-guard-windows-guide.md >checksums.txt)
cmp -s "$work/local/checksums.txt" "$work/checksums.txt" ||
	{ echo "CI's checksums.txt differs from one built here from $tag; not signing" >&2; diff "$work/local/checksums.txt" "$work/checksums.txt" >&2 || true; exit 1; }
echo "checksums.txt: matches a local rebuild of every published file"

if $check_only; then
	echo "== $tag: release matches the tag"
	exit 0
fi

echo "== sign"
go run ./cmd/rgsign -key "$key" "$work/local/checksums.txt"
go run ./cmd/rgsign -verify "$work/local/checksums.txt"
gh release upload "$tag" "$work/local/checksums.txt.sig" -R "$repo"
gh release edit "$tag" --draft=false -R "$repo" >/dev/null
echo "== $tag signed and published; release-check runs the update and install tests"
