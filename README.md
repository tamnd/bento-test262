# bento-test262

Runs the official [tc39/test262](https://github.com/tc39/test262) conformance suite against the ahead-of-time path of [bento](https://github.com/tamnd/bento).

Every job goes through the same pipeline `bento build` ships: type-check and lower the test to Go source, compile that Go with the toolchain, run the native binary, judge the outcome.
There is no interpreter in the loop.
The point is to measure exactly what the compiled subset can and cannot do, and to turn the gap into a ranked work order for the lowerer.

> [!WARNING]
> A full run can OOM and hard-reboot the machine. This has happened.
>
> Every job holds a typescript-go checker, and each `go build` fans out to
> `GOMAXPROCS` compile processes. Left unbounded, a wide pool of either climbs
> past the RAM the box has and the kernel reboots before it can page out. Two
> failure modes fed the crash we saw:
>
> 1. **Checker leak across jobs.** A worker serves many jobs from one process,
>    and the checker retains per-program memory between them, so a long-lived
>    worker's resident set climbs without bound (one sequential worker walked
>    from ~215 MB to over 8 GB across ~2000 jobs).
> 2. **`go build` compile fan-out.** `go build` defaults `-p` to `GOMAXPROCS`,
>    so on a 24-core box one build spawns ~24 parallel compilers at ~400 MB
>    each, and several workers building at once multiply that into tens of GB
>    of transient memory.
>
> The runner now guards both by default: it recycles a worker after
> `-worker-max-jobs` jobs, sets a soft `GOMEMLIMIT` sized to a fraction of
> total RAM (per process and per worker), and caps `go build -p`. Keep those
> guards on. On a memory-tight box (24 GB or less), run with a small `-jobs`
> (2 is safe), prefer `-lower-only` with `-limit` for quick checks, and push a
> full build-and-run to a larger machine. See
> [Running safely](#running-safely) before you start a run, and never lower the
> guards below without watching resident memory.
>
> A few tests are quarantined outright in `expectations/denylist.txt` because
> their lowering crashes the toolchain or exhausts memory hard enough to threaten
> the machine. Do not remove one until the underlying gap is fixed.

## Statuses

The AOT path can decline a program or claim it, and the split matters:

- `pass` — lowered, compiled, ran, behaved as the test demands.
- `handback` — the front end or the lowerer declined the program. This is the honest edge of the compiled subset: a coverage gap, not a bug.
- `fail` — the AOT path claimed the program and got it wrong. Emitted Go that does not compile, a binary that misbehaves. Always a bento bug.
- `timeout` / `crash` — the binary overran its budget, or the harness broke.

Growing `pass` at the expense of `handback` is the campaign.
Any `fail` is a fix-now item.

## Layout

- `test262/` is a pinned submodule of the upstream suite.
- `harness/` holds TypeScript ports of the upstream harness files. The checker has to accept the prelude before any test body gets a verdict, and the upstream files lean on idioms it rejects (constructor functions growing properties, a redeclared `print`), so each include used by a test needs a port here. A test whose include has no port yet is reported as `handback` with the missing name, which makes porting order a measurable decision.
- `cmd/bento262` is the runner.
- `pkg/tc39` is the harness: frontmatter parsing, source composition, the AOT pipeline, the worker pool, the results cache, expectations.
- `expectations/statuses.txt` is the committed snapshot of every non-passing job. The suite is green when reality matches it exactly, in both directions; improvements are recorded on purpose with `-update`, never by accident.

## Running

```
git submodule update --init --depth 1
go build -o bin/bento262 ./cmd/bento262
./bin/bento262 run
```

A run discovers every test under `test/language`, `test/built-ins`, and `test/harness`, expands each into a sloppy and a strict execution unless flags say otherwise, and fans the jobs out to worker subprocesses.
Each job composes its own program: `"use strict";` for the strict pass, the `sta` and `assert` ports, ports of the test's `includes`, the `$DONE` handler for async tests, then the test body.
The composed file rides in under a `.ts` name because that is the AOT front door; where the checker disagrees with sloppy JavaScript, the job lands in `handback`, which is the truthful status today.

Useful flags:

- `-filter test/built-ins/Array` runs a slice of the tree (comma separated for several).
- `-update` rewrites `expectations/statuses.txt` from this run.
- `-v` prints the error behind each regression.
- `-jobs N`, `-run-timeout D`, `-job-timeout D` size the pool and budgets.

The summary ends with the top handback and fail reasons by job count.
That table is the priority queue: the biggest handback reason is the lowering gap whose fix moves the most tests.

## Running safely

Read the warning at the top first. A run is memory-heavy on both halves: the
front half holds a checker per in-flight job, and the back half spawns parallel
compilers per build. The runner ships with guards on by default so an ordinary
run stays inside the machine's RAM, and the flags below tune them.

- `-jobs N` bounds how many workers run at once, and with them the resident
  checkers and the concurrent builds. On a 24 GB box, `-jobs 2` is the safe
  ceiling. This is the single most important dial.
- `-worker-max-jobs N` (default 200) recycles a worker after it serves `N`
  jobs, which resets the checker memory it has accumulated. `0` disables
  recycling; do not set it to `0` on a memory-tight box.
- A soft `GOMEMLIMIT` is set automatically to a fraction of total RAM, both for
  the runner process and, divided by the worker count, for each worker. Override
  the fraction with `BENTO262_MEM_FRACTION` (default `0.5`), or pin `GOMEMLIMIT`
  in the environment to take over entirely. The limit makes the runtime collect
  before the resident set runs away; it is a backstop, not a substitute for a
  sane `-jobs`.
- `go build -p` is capped (default 2) so one build cannot fan out to
  `GOMAXPROCS` compilers. Override with `BENTO262_GO_BUILD_P`.
- `-lower-only` runs the front half only: it lowers every job in process and
  reports the lowered/handback split without building or running a binary. It is
  the cheap, disk-safe way to size a lowerer change. It still holds checkers, so
  bound it with `-jobs`, and pair it with `-limit` or `-grep` for a quick slice.
  Unlike the full run it does not recycle a worker, so the checker's memory
  accumulates in the one process and `GOMEMLIMIT` cannot collect it; a full-suite
  lower-only would be OOM-killed before it reports. The runner refuses an unscoped
  lower-only past a large job count for that reason, so scope it or use the full
  run, which recycles workers.
- `-min-free-disk-mb` (default 3072) aborts before staging if the cache
  filesystem is low, so a full run cannot fill the disk mid-build.
- `-stuck-after` (default 90s) names any job still running past that long, so a
  stall points at the wedging test instead of going quiet. Set it to `0` to
  silence the watchdog.

For a heavy full run, prefer a box with plenty of RAM and cores over the local
Mac. On the Mac, stick to `-lower-only` with a `-limit`, or a narrow `-filter`
with `-jobs 2`, and watch resident memory the first time you run a new slice.

### Quarantine

`expectations/denylist.txt` lists tests that must never reach a worker: a test
whose lowering or build is known to exhaust memory or wedge the toolchain badly
enough to threaten the machine. A denylisted line is not a failing test; it is a
landmine skipped up front so a crash can never wedge a worker slot. Each entry
is a path substring matched the same way `-grep` matches, with a note saying why
it is dangerous. Quarantined tests are dropped before anything spawns and left
out of the tallies entirely, so they do not appear in the snapshot. Point at a
different file with `-denylist`. Remove an entry only when the underlying gap is
fixed and a run confirms the test is safe.

## Caching

Every result is cached in `.cache/results-<bento version>.ndjson`, keyed by a hash of the bento version and the exact composed source.
A rerun only executes jobs whose inputs changed; bumping bento in `go.mod` invalidates everything, editing one harness port invalidates just the tests that include it.
Timeouts and crashes are never cached.
CI restores the newest cache file and saves the grown one after each run, so a pull request that touches nothing pays close to zero execution time.

The cache is written as each result arrives, not once at the end, so a run is resumable.
If a run is interrupted (Ctrl-C, a kill, or a crash) the jobs it already finished are on disk, and rerunning the same command replays them from the cache and continues with the rest.
A `Ctrl-C` drains the in-flight jobs and exits cleanly with a partial summary rather than dropping the run; a second `Ctrl-C` force-kills.
This is the way to run the whole suite when a single pass is too long to sit through: start it, stop it when you need the machine, rerun it later, and it picks up where it left off.

A run that stalls is not silent.
`-stuck-after` (default 90s) names any job still running past that long, so a wedged build or a hang points at the exact test rather than leaving the run looking merely slow.
An interrupted run also prints the jobs that were still in flight when it stopped.
A test that reliably wedges a worker belongs on the denylist; the name the watchdog prints is what to add.

The staged bento module under `.cache/bento-<version>/` is a writable copy of the pinned module.
Generated programs are built inside it so their import of the runtime resolves against the exact version the harness links, with no network, and the shared Go build cache makes each per-test build a link step.

## Reusing the runner binary

The runner lowers every test in process, so its binary embeds bento's lowering.
Reusing an old binary after a lowering edit would measure the wrong compiler, which is why a fresh build is needed whenever bento changes.

`scripts/runner.sh` makes reuse safe by keying a cached binary on a hash of the bento checkout it links, the committed rev plus the working-tree diff.
An unchanged tree hits the cache and copies the binary to `bin/bento262` in a few milliseconds; any edit lands on a fresh key and builds once.
The base and fixed sides of an A/B get two different keys, so each side is built once and reused on every rerun.
The cache lives under `$HOME/.cache/bento262`, overridable with `BENTO262_RUNNER_CACHE`.

Keep the default Go build cache warm rather than pointing `GOCACHE` at a throwaway directory.
The typescript-go checker is the heavy part of the build, and a warm cache keeps it a content hit so the runner build stays a link step.
A fresh throwaway cache recompiles the whole checker per build, which is slow and can exhaust memory when several builds run at once.
For the same reason, run with a small `-jobs` when measuring locally; every job links a static binary, and a wide pool of those peaks a lot of memory.

## What counts as a pass

A normal test passes when the binary exits clean.
An `async` test must also print `Test262:AsyncTestComplete`.
A `negative` test must be rejected: for phase `parse` or `resolution` a build error is the AOT compiler's early error; for phase `runtime` the binary must die mentioning the expected error type.
`intl402`, `annexB`, and `staging` are out of scope.

## Known structural gaps

There is no `$262` host object; tests that touch it are handed back by the checker with a name error until bento grows host hooks.
Module tests run through the same single-file front door for now, so most sit in `handback`.
The `sta` port diverges from upstream in one corner: calling `Test262Error` without `new` throws instead of constructing, which no current test relies on.
