# bento-test262

Runs the official [tc39/test262](https://github.com/tc39/test262) conformance suite against the ahead-of-time path of [bento](https://github.com/tamnd/bento).

Every job goes through the same pipeline `bento build` ships: type-check and lower the test to Go source, compile that Go with the toolchain, run the native binary, judge the outcome.
There is no interpreter in the loop.
The point is to measure exactly what the compiled subset can and cannot do, and to turn the gap into a ranked work order for the lowerer.

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

## Caching

Every result is cached in `.cache/results-<bento version>.ndjson`, keyed by a hash of the bento version and the exact composed source.
A rerun only executes jobs whose inputs changed; bumping bento in `go.mod` invalidates everything, editing one harness port invalidates just the tests that include it.
Timeouts and crashes are never cached.
CI restores the newest cache file and saves the grown one after each run, so a pull request that touches nothing pays close to zero execution time.

The staged bento module under `.cache/bento-<version>/` is a writable copy of the pinned module.
Generated programs are built inside it so their import of the runtime resolves against the exact version the harness links, with no network, and the shared Go build cache makes each per-test build a link step.

## What counts as a pass

A normal test passes when the binary exits clean.
An `async` test must also print `Test262:AsyncTestComplete`.
A `negative` test must be rejected: for phase `parse` or `resolution` a build error is the AOT compiler's early error; for phase `runtime` the binary must die mentioning the expected error type.
`intl402`, `annexB`, and `staging` are out of scope.

## Known structural gaps

There is no `$262` host object; tests that touch it are handed back by the checker with a name error until bento grows host hooks.
Module tests run through the same single-file front door for now, so most sit in `handback`.
The `sta` port diverges from upstream in one corner: calling `Test262Error` without `new` throws instead of constructing, which no current test relies on.
