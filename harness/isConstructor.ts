// TypeScript port of test262/harness/isConstructor.js. A test uses isConstructor
// to decide whether a value can be called with `new`, by attempting a
// Reflect.construct with the value as the newTarget and treating a throw as the
// not-a-constructor answer. The logic and the Test262Error message are kept
// faithful to upstream; the only change for the front door is the `any`
// annotation on the parameter, so the checker admits the JS-as-TS source instead
// of flagging an implicit any. The Reflect.construct three-argument form lowers
// since phase 10's Reflect group.
function isConstructor(f: any): any {
    if (typeof f !== "function") {
      throw new Test262Error("isConstructor invoked with a non-function value");
    }

    try {
        Reflect.construct(function(){}, [], f);
    } catch (e) {
        return false;
    }
    return true;
}
