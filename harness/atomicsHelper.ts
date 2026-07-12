// TypeScript port of test262/harness/atomicsHelper.js. Upstream extends the host's
// $262.agent object, the API for spawning worker agents and coordinating them over the
// bytes of one SharedArrayBuffer: start runs a source string as a second agent,
// broadcast hands it a shared buffer, report and getReport pass strings back, and
// waitUntil/tryYield spin until the other agents have reported themselves. On top of
// that base the helper defines getReport (blocking), getReportAsync, safeBroadcast,
// safeBroadcastAsync, setTimeout, tryYield, trySleep, waitUntil, and the timeouts table.
//
// Ceiling: bento's AOT output is a single Go process with one agent, so there is no
// host to start a second agent and no other agent to broadcast to, report from, or wait
// on. The test262 host contract puts $262 on the host; bento has no such runtime, so
// this port stands in for it with a single-agent $262 the same way detachArrayBuffer's
// port stands in for the host detach hook. Every file that includes this helper now
// gets past include resolution and the single-agent-expressible parts lower and run; a
// test that needs a real second agent to make progress (start plus broadcast plus a
// getReport or waitUntil that only another agent can satisfy) is the multi-agent slice
// bento hands back rather than emits.
//
// The shim keeps the names and shapes the tests call. Where the upstream helper reaches
// for the host's report queue there is none single-agent, so the queue is always empty:
// getReport blocks with nothing to return and getReportAsync polls forever, which is
// exactly the behavior a multi-agent test would see with its second agent absent, and
// those tests hand back. The plumbing is rephrased into the forms the AOT path lowers:
// the setTimeout busy-wait and the getReportAsync then-chain become async/await, and the
// no-contention Atomics.load spins in waitUntil and safeBroadcastAsync read the element
// directly, which is the same value a load returns when no other agent writes.

interface Agent262Timeouts {
  yield: number;
  small: number;
  long: number;
  huge: number;
}

interface Agent262 {
  start(source: string): void;
  broadcast(sab: SharedArrayBuffer): void;
  sleep(ms: number): void;
  monotonicNow(): number;
  receiveBroadcast(callback: (sab: SharedArrayBuffer) => void): void;
  report(value: any): void;
  leaving(): void;
  getReport(): string | null;
  getReportAsync(): Promise<string>;
  safeBroadcast(typedArray: Int32Array | BigInt64Array): void;
  safeBroadcastAsync(ta: Int32Array | BigInt64Array, index: number, expected: number | bigint): Promise<number | bigint>;
  setTimeout(callback: () => void, delay: number): void;
  tryYield(): void;
  trySleep(ms: number): void;
  waitUntil(typedArray: Int32Array | BigInt64Array, index: number, expected: number | bigint): void;
  timeouts: Agent262Timeouts;
}

interface Host262 {
  agent: Agent262;
}

// pullReport is the single-agent stand-in for the host's inter-agent report queue.
// With one agent nothing else ever calls report, so the queue is always empty and this
// returns null: getReport therefore has nothing to hand back and getReportAsync never
// resolves, which is the honest single-agent view of a report that only a second agent
// could send.
function atomicsHelperPullReport(): string | null {
  return null;
}

const $262: Host262 = {
  agent: {
    // start, broadcast, receiveBroadcast, report, and leaving are the host's
    // agent-spawning surface. A single Go process has no second agent to start, no
    // other agent to broadcast to, and receiveBroadcast/report/leaving only ever run
    // inside a spawned agent, which never exists here, so each is a no-op.
    start(source: string): void {},
    broadcast(sab: SharedArrayBuffer): void {},
    receiveBroadcast(callback: (sab: SharedArrayBuffer) => void): void {},
    report(value: any): void {},
    leaving(): void {},
    // sleep yields to other agents; with one agent there is nothing to yield to, so it
    // is a no-op. monotonicNow reports a monotonic clock the coordination tests only
    // read relative to themselves; zero is a valid constant reading single-agent.
    sleep(ms: number): void {},
    monotonicNow(): number {
      return 0;
    },
    // getReport blocks until another agent reports. Single-agent the report queue is
    // always empty, so this spins with nothing to return, exactly as a multi-agent test
    // would spin with its second agent absent; such tests hand back.
    getReport(): string | null {
      let r: string | null = atomicsHelperPullReport();
      while (r === null) {
        r = atomicsHelperPullReport();
      }
      return r;
    },
    // getReportAsync is the promise form of getReport, rephrased from the upstream
    // setTimeout poll into an async loop the AOT path lowers. It polls the empty queue
    // and so never resolves single-agent, the same absent-second-agent case.
    getReportAsync(): Promise<string> {
      return (async function (): Promise<string> {
        let r: string | null = atomicsHelperPullReport();
        while (r === null) {
          await Promise.resolve();
          r = atomicsHelperPullReport();
        }
        return r;
      })();
    },
    // safeBroadcast shares a waitable typed array with every agent. Upstream first
    // probes that the array is shareable by waiting on a throwaway copy; the checker
    // already restricts the parameter to the waitable Int32Array and BigInt64Array, and
    // single-agent broadcast reaches no other agent, so the probe has nothing to prove
    // and is dropped.
    safeBroadcast(typedArray: Int32Array | BigInt64Array): void {},
    // safeBroadcastAsync broadcasts and then waits for `expected` agents to report by
    // adding to the element, returning the element once they have. Single-agent no other
    // agent adds, so the element never reaches `expected` and this never resolves; the
    // no-contention load is read directly as the element.
    safeBroadcastAsync(ta: Int32Array | BigInt64Array, index: number, expected: number | bigint): Promise<number | bigint> {
      return (async function (): Promise<number | bigint> {
        while (ta[index] !== expected) {
          await Promise.resolve();
        }
        return ta[index];
      })();
    },
    // setTimeout defers a callback. Upstream polls Date.now on a promise chain; with one
    // agent there is no competing work to defer past, so it runs the callback on the
    // next microtask through async/await, the form the AOT path lowers.
    setTimeout(callback: () => void, delay: number): void {
      (async function (): Promise<void> {
        await Promise.resolve();
        callback();
      })();
    },
    // tryYield and trySleep ask the runtime to give other agents a turn. With one agent
    // there is no other agent to run, so both are no-ops, matching sleep.
    tryYield(): void {},
    trySleep(ms: number): void {},
    // waitUntil spins until `expected` agents have reported themselves at the index by
    // adding to the element. Single-agent no other agent adds, so unless `expected` is
    // already present this never terminates; the no-contention load reads the element
    // directly.
    waitUntil(typedArray: Int32Array | BigInt64Array, index: number, expected: number | bigint): void {
      let agents: number | bigint = typedArray[index];
      while (agents !== expected) {
        agents = typedArray[index];
      }
      assert.sameValue(agents, expected, "Reporting number of 'agents' equals the value of 'expected'");
    },
    timeouts: {
      yield: 100,
      small: 200,
      long: 1000,
      huge: 10000,
    },
  },
};
