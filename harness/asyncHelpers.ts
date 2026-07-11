// TypeScript port of test262/harness/asyncHelpers.js. The upstream file defines
// asyncTest, which drives the sole asynchronous test of a file to $DONE, and
// assert.throwsAsync, which asserts a callback's returned promise rejects with a
// given error constructor. The logic and the failure messages are kept faithful.
// The promise plumbing is rephrased from the upstream two-argument
// then(onFulfilled, onRejected) into an async wrapper that awaits the callback
// under try/catch, the form the AOT path lowers: awaiting a rejected promise
// throws the rejection into the catch, which is the same fulfil-or-reject split
// the upstream then arms observe. A test carrying this include always sets the
// async flag, so $DONE is in scope through doneprintHandle; the upstream
// globalThis presence check has no meaning with no host reflection in the
// compiled subset and is dropped, so $DONE is simply always declared by the time
// asyncTest runs. The callback is typed as a function returning a promise rather
// than left untyped, so its awaited result stays on the static promise path
// instead of a dynamic then the subset declines.

// throwsAsync is a method the async prelude adds to assert, so the Assert
// interface is widened by declaration merging rather than edited in the base
// assert port.
interface Assert {
  throwsAsync(expectedErrorConstructor: any, func: () => Promise<void>, message?: any): Promise<void>;
}

function asyncTest(testFunc: () => Promise<void>): void {
  (async function (): Promise<void> {
    try {
      await testFunc();
      $DONE();
    } catch (error: any) {
      $DONE(error);
    }
  })();
}

assert.throwsAsync = function (expectedErrorConstructor: any, func: () => Promise<void>, message?: any): Promise<void> {
  const expectedName: any = expectedErrorConstructor.name;
  const expectation = "Expected a " + expectedName + " to be thrown asynchronously";
  return (async function (): Promise<void> {
    const fail = function (detail: any): void {
      if (message === undefined) {
        throw new Test262Error(detail);
      }
      throw new Test262Error(message + " " + detail);
    };
    let threw = false;
    let thrown: any;
    try {
      await func();
    } catch (caught: any) {
      threw = true;
      thrown = caught;
    }
    if (!threw) {
      fail(expectation + " but no exception was thrown at all");
    } else if (thrown === null || typeof thrown !== "object") {
      fail(expectation + " but thrown value was not an object");
    } else if (thrown.constructor !== expectedErrorConstructor) {
      const actualName: any = thrown.constructor.name;
      if (expectedName === actualName) {
        fail(expectation + " but got a different error constructor with the same name");
      } else {
        fail(expectation + " but got a " + actualName);
      }
    }
  })();
};
