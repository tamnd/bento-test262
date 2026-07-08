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

# Refuse to start when the disk is nearly full. Every built test caches a
# multi-megabyte linked binary in GOCACHE, so a run that begins with little
# headroom fills the volume, and go build dies with "signal: killed" or "no
# space left on device" partway through. Fail fast with a clear message instead.
# Override the floor with BENTO262_MIN_FREE_GB (default 15).
min_free_gb="${BENTO262_MIN_FREE_GB:-15}"
avail_kb="$(df -Pk "$root" | awk 'NR==2 {print $4}')"
free_gb=$(( avail_kb / 1024 / 1024 ))
if [ -n "$avail_kb" ] && [ "$free_gb" -lt "$min_free_gb" ]; then
  echo "runner: only ${free_gb}G free on the volume, need ${min_free_gb}G." >&2
  echo "runner: free space (e.g. trim ~/Library/Caches/go-build) before running." >&2
  exit 1
fi

# Resolve the bento module this harness builds against, honoring the go.mod
# replace so a local checkout is what we hash and build.
bento="$(cd "$root" && go list -m -f '{{.Dir}}' github.com/tamnd/bento)"

# Hash the committed rev plus the working-tree diff, so a dirty edit gets its
# own key instead of colliding with the clean commit. Fold in the harness's own
# source state too: the binary embeds this repo's lowering driver and build
# glue, so an edit here (say to how the scratch package is named) must get a
# fresh binary rather than silently reuse one built from the old code.
rev="$(git -C "$bento" rev-parse HEAD 2>/dev/null || echo nogit)"
dirty="$(git -C "$bento" diff 2>/dev/null | shasum | cut -d ' ' -f 1)"
hrev="$(git -C "$root" rev-parse HEAD 2>/dev/null || echo nogit)"
hdirty="$(git -C "$root" diff 2>/dev/null | shasum | cut -d ' ' -f 1)"
bin="$cache/bento262-${rev}-${dirty}-${hrev}-${hdirty}"

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
