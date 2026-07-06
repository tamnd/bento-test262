// TypeScript port of test262/harness/doneprintHandle.js. Upstream routes the
// completion line through the host's print; here it goes straight to
// console.log, which is the same stdout the judge reads, and sidesteps the
// print declaration the checker's ambient libs already own.
function __consolePrintHandle__(msg: any): void {
  console.log(msg);
}

function $DONE(error?: any): void {
  if (error) {
    if (typeof error === 'object' && error !== null && 'name' in error) {
      __consolePrintHandle__('Test262:AsyncTestFailure:' + error.name + ': ' + error.message);
    } else {
      __consolePrintHandle__('Test262:AsyncTestFailure:Test262Error: ' + String(error));
    }
  } else {
    __consolePrintHandle__('Test262:AsyncTestComplete');
  }
}
