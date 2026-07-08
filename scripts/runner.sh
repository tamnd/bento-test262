#!/usr/bin/env bash
# Build the bento262 runner keyed by the bento checkout it links, and reuse a
# cached binary when that checkout has not changed.
#
# The runner lowers every test in process: it links pkg/build and calls the
# compiler directly rather than shelling out. So the binary embeds bento's
# lowering, and reusing an old binary after a lowering edit would measure the
# wrong compiler. Keying the cache by a hash of the bento tree makes reuse safe:
# an unchanged tree hits the cache, and any edit lands on a fresh key. The base
# and fixed sides of an A/B naturally get two different binaries.
#
# Prints the cached binary path and copies it to bin/bento262. Set
# BENTO262_RUNNER_CACHE to move the cache off the default under $HOME/.cache.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cache="${BENTO262_RUNNER_CACHE:-$HOME/.cache/bento262}"

# Resolve the bento module this harness builds against, honoring the go.mod
# replace so a local checkout is what we hash and build.
bento="$(cd "$root" && go list -m -f '{{.Dir}}' github.com/tamnd/bento)"

# Hash the committed rev plus the working-tree diff, so a dirty edit gets its
# own key instead of colliding with the clean commit.
rev="$(git -C "$bento" rev-parse HEAD 2>/dev/null || echo nogit)"
dirty="$(git -C "$bento" diff 2>/dev/null | shasum | cut -d ' ' -f 1)"
bin="$cache/bento262-${rev}-${dirty}"

if [ ! -x "$bin" ]; then
  mkdir -p "$cache"
  # No GOCACHE override on purpose. The default persistent cache keeps the
  # typescript-go checker a content hit, so this is a link step rather than a
  # full recompile, which is what keeps the build cheap and off the OOM edge.
  ( cd "$root" && go build -o "$bin" ./cmd/bento262 )
fi

mkdir -p "$root/bin"
cp "$bin" "$root/bin/bento262"
echo "$bin"
