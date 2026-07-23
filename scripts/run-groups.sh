#!/usr/bin/env bash
# Run the test262 corpus one directory group at a time, top to bottom, with a
# per-group checkpoint so the sweep is resumable and skips groups it already
# finished.
#
# Why a group runner on top of `bento262 run`. The runner already caches
# results per bento version, streams them so an interrupt loses nothing, and
# pins the go build cache flat, so a single `bento262 run` is itself resumable
# and disk-safe. What it does not do is pace the whole corpus: an unscoped run
# is one 70k-job push that fills a terminal, holds a machine for hours, and
# gives no partial ledger to read along the way. This script slices the corpus
# into its directory groups (the audit's working order: one second-level dir
# under test/language, test/built-ins, and test/harness is one group), runs
# each with --grep so its scope is small and its result is a line you can read,
# and records a checkpoint per group so a rerun picks up at the next unfinished
# group instead of starting the corpus over.
#
# What you get:
#   incremental  the results cache is shared across groups (same bento version),
#                so a group already run in a prior invocation replays from cache;
#                the tail cache carries any job whose lowering output is unchanged
#                across a bento edit, so a delta run only rebuilds what moved.
#   grouped      groups run in path order, one at a time, each a bounded slice.
#   resumable    a finished group writes a .done checkpoint; a rerun skips it. An
#                interrupt mid-group is caught by `bento262 run` itself (progress
#                streamed to the cache), so rerunning resumes inside the group too.
#   cached       results-<version>.ndjson and the shared tail.ndjson are reused;
#                the checkpoint dir is keyed by the exact bento tree, so a lowering
#                edit starts a fresh ledger rather than trusting stale checkpoints.
#   disk-clean   every group run wipes and re-warms its dedicated go build cache
#                and pins it to the dependency floor, so the footprint stays flat
#                no matter how many groups run.
#
# Usage:
#   scripts/run-groups.sh [--from GROUP] [--only GROUP] [--list] [--reset]
#                         [--jobs N] [-- EXTRA bento262 run flags]
#
#   --list         print the group order and each group's checkpoint state, then exit.
#   --from GROUP    start at the first group whose path contains GROUP (skip earlier).
#   --only GROUP    run just the groups whose path contains GROUP.
#   --reset         clear this bento tree's checkpoints before running.
#   --jobs N        worker subprocesses per group (default: the runner's own default).
#   --               everything after is passed through to `bento262 run` verbatim.
#
# State lives under .groups/<bento-rev>-<tree-hash>/: order.txt is the frozen group
# order, <slug>.log is a group's full run output, <slug>.done is its checkpoint and
# holds the group's TOTAL summary line.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

filters="test/language,test/built-ins,test/harness"
test_root="test262"
from=""
only=""
do_list=0
do_reset=0
jobs=""
passthrough=()

while [ $# -gt 0 ]; do
  case "$1" in
    --from)   from="$2"; shift 2 ;;
    --only)   only="$2"; shift 2 ;;
    --list)   do_list=1; shift ;;
    --reset)  do_reset=1; shift ;;
    --jobs)   jobs="$2"; shift 2 ;;
    --)       shift; passthrough=("$@"); break ;;
    *) echo "run-groups: unknown argument $1" >&2; exit 2 ;;
  esac
done

# Build the runner keyed by the bento checkout it links, and learn the bento tree
# it built against so the checkpoint dir tracks the exact compiler. runner.sh
# prints the cached binary path on its last line; the key we want is the bento
# rev plus its working-tree diff, the same pair runner.sh keys its own cache by,
# so an edit to the lowerer lands on a fresh ledger instead of reusing checkpoints
# from the old compiler.
echo "run-groups: building the runner..." >&2
./scripts/runner.sh >/dev/null
bento_dir="$(go list -m -f '{{.Dir}}' github.com/tamnd/bento)"
bento_rev="$(git -C "$bento_dir" rev-parse --short HEAD 2>/dev/null || echo nogit)"
bento_dirty="$(git -C "$bento_dir" diff 2>/dev/null | shasum | cut -c1-8)"
state=".groups/${bento_rev}-${bento_dirty}"

if [ "$do_reset" = 1 ]; then
  rm -rf "$state"
  echo "run-groups: cleared checkpoints under $state" >&2
fi
mkdir -p "$state"

# Freeze the group order once per bento tree so a resume walks the same list even
# if the corpus is repinned mid-campaign. A group is a directory one level under a
# filter prefix that holds at least one .js test; a prefix that holds loose tests
# directly (test/harness does) is itself one group. The order is path-sorted, which
# is the top-to-bottom order the audit works the corpus in.
order="$state/order.txt"
if [ ! -f "$order" ]; then
  : > "$order"
  IFS=',' read -ra prefixes <<< "$filters"
  for prefix in "${prefixes[@]}"; do
    base="$test_root/$prefix"
    [ -d "$base" ] || continue
    # Directory groups: each immediate subdir that contains a test.
    while IFS= read -r d; do
      rel="${d#"$test_root"/}"
      echo "$rel"
    done < <(find "$base" -maxdepth 1 -mindepth 1 -type d | sort)
    # Loose tests sitting directly under the prefix are their own group, named by
    # the prefix, so no test is left out of a group.
    if find "$base" -maxdepth 1 -type f -name '*.js' | grep -q .; then
      echo "$prefix#loose"
    fi
  done >> "$order"
fi

slug() { echo "$1" | tr '/#' '__'; }

if [ "$do_list" = 1 ]; then
  echo "group order for bento $bento_rev (dirty $bento_dirty), state $state:"
  while IFS= read -r g; do
    s="$(slug "$g")"
    if [ -f "$state/$s.done" ]; then
      printf "  [done] %-48s %s\n" "$g" "$(cat "$state/$s.done")"
    else
      printf "  [    ] %s\n" "$g"
    fi
  done < "$order"
  exit 0
fi

# Walk the frozen order, honoring --from (skip until the marker matches) and --only
# (run only matching groups). Each group runs as its own `bento262 run --grep`, so
# its scope is the group and only the group. --update writes a per-group snapshot
# under expectations/by-group/, which never clobbers another group's ledger, and the
# TOTAL line the run prints is saved as the checkpoint.
started=0
[ -z "$from" ] && started=1
ran=0
mkdir -p expectations/by-group

while IFS= read -r g; do
  grep_arg="${g%#loose}"
  s="$(slug "$g")"

  if [ "$started" = 0 ]; then
    case "$g" in *"$from"*) started=1 ;; *) continue ;; esac
  fi
  if [ -n "$only" ]; then
    case "$g" in *"$only"*) ;; *) continue ;; esac
  fi

  if [ -f "$state/$s.done" ]; then
    printf "run-groups: skip %-46s %s\n" "$g" "(done: $(cat "$state/$s.done"))"
    continue
  fi

  echo "run-groups: === $g ===" >&2
  args=(run --grep "$grep_arg" --update --expectations "expectations/by-group/$s.txt")
  [ -n "$jobs" ] && args+=(--jobs "$jobs")
  args+=("${passthrough[@]}")

  # Tee the run to the group log so a stall or a crash leaves a full trace, and let
  # the exit status through: a nonzero (a wedged toolchain, out of disk) must stop
  # the sweep rather than checkpoint a group that never really ran.
  set +e
  ./bin/bento262 "${args[@]}" 2>&1 | tee "$state/$s.log"
  rc="${PIPESTATUS[0]}"
  set -e
  if [ "$rc" != 0 ]; then
    echo "run-groups: group $g exited $rc, stopping (rerun to resume here)" >&2
    exit "$rc"
  fi

  total="$(grep -E '^TOTAL' "$state/$s.log" | tail -1 | sed -E 's/^TOTAL[[:space:]]*//')"
  [ -z "$total" ] && total="(no TOTAL line; see $s.log)"
  echo "$total" > "$state/$s.done"
  printf "run-groups: done %-46s %s\n" "$g" "$total"
  ran=$((ran + 1))
done < "$order"

# Aggregate every checkpoint into one corpus-wide tally, so the sweep ends with the
# same pass/handback/fail shape a single unscoped run would print, assembled from the
# groups instead of one monolithic push.
echo
echo "run-groups: ran $ran group(s) this invocation. corpus tally from checkpoints:"
awk '
  match($0, /([0-9]+) pass/, a)      { pass += a[1] }
  match($0, /handback ([0-9]+)/, h)  { hb += h[1] }
  match($0, /fail ([0-9]+)/, f)      { fl += f[1] }
  match($0, /crash ([0-9]+)/, c)     { cr += c[1] }
  match($0, /timeout ([0-9]+)/, t)   { to += t[1] }
  END { printf "  %d pass, handback %d, fail %d, crash %d, timeout %d\n", pass, hb, fl, cr, to }
' "$state"/*.done 2>/dev/null || echo "  (no checkpoints yet)"
