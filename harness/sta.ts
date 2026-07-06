// TypeScript port of test262/harness/sta.js. The AOT front door type-checks
// its input, and the upstream constructor-function idiom does not check, so
// the harness carries this typed equivalent. One known divergence: calling
// Test262Error without new returns an instance upstream and throws here; no
// current test leans on that.
class Test262Error {
  message: string;
  constructor(message?: any) {
    this.message = message || "";
  }
  toString(): string {
    return "Test262Error: " + this.message;
  }
  static thrower(message?: any): never {
    throw new Test262Error(message);
  }
}

function $DONOTEVALUATE(): never {
  throw "Test262: This statement should not be evaluated.";
}
