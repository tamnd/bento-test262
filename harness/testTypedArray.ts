// TypeScript port of test262/harness/testTypedArray.js, the second-highest-reach
// include in the suite. It defines the typed-array constructor lists and the
// testWith*TypedArrayConstructors drivers a test uses to sweep a body across every
// TypedArray constructor and argument-factory shape. The logic and the messages
// are kept faithful to upstream; two shapes change for the front door, neither
// altering behaviour on any real test: every binding carries an `any` annotation
// so the checker admits the JS-as-TS source instead of flagging an implicit any,
// and the two error-only `Test262Error(...)` calls take `new`, since sta.ts models
// Test262Error as a class (the same divergence sta.ts already documents; both
// calls sit on misuse paths a passing test never reaches). isPrimitive comes from
// assert.ts, so it is not redefined here. The first-class typed-array constructor
// dispatch (`new TA(n)` off a constructor pulled from an array, `Object.getPrototypeOf(Int8Array)`),
// `.bind` partial application, `Array.from` with a map function, `BYTES_PER_ELEMENT`,
// and mutating a caught error's message before a rethrow all lower as written.

var floatArrayConstructors: any = [
  Float64Array,
  Float32Array
];

var nonClampedIntArrayConstructors: any = [
  Int32Array,
  Int16Array,
  Int8Array,
  Uint32Array,
  Uint16Array,
  Uint8Array
];

var intArrayConstructors: any = nonClampedIntArrayConstructors.concat([Uint8ClampedArray]);

// Float16Array is a newer feature
// adding it to this list unconditionally would cause implementations lacking it to fail every test which uses it
if (typeof Float16Array !== "undefined") {
  floatArrayConstructors.push(Float16Array);
}

var bigIntArrayConstructors: any = [];
if (typeof BigInt64Array !== "undefined") {
  bigIntArrayConstructors.push(BigInt64Array);
}
if (typeof BigUint64Array !== "undefined") {
  bigIntArrayConstructors.push(BigUint64Array);
}

/**
 * Array containing every non-bigint typed array constructor.
 */
var typedArrayConstructors: any = floatArrayConstructors.concat(intArrayConstructors);

/**
 * Array containing every typed array constructor, including those with bigint values.
 */
var allTypedArrayConstructors: any = typedArrayConstructors.concat(bigIntArrayConstructors);

/**
 * The %TypedArray% intrinsic constructor function.
 */
var TypedArray: any = Object.getPrototypeOf(Int8Array);

function makePassthrough(TA: any, primitiveOrIterable: any): any {
  return primitiveOrIterable;
}

function makeArray(TA: any, primitiveOrIterable: any): any {
  if (isPrimitive(primitiveOrIterable)) {
    var n: any = Number(primitiveOrIterable);
    // Only values between 0 and 2**53 - 1 inclusive can get mapped into TA contents.
    if (!(n >= 0 && n < 9007199254740992)) return primitiveOrIterable;
    return Array.from({ length: n }, function(): any { return "0"; });
  }
  return Array.from(primitiveOrIterable);
}

function makeArrayLike(TA: any, primitiveOrIterable: any): any {
  var arr: any = makeArray(TA, primitiveOrIterable);
  if (isPrimitive(arr)) return arr;
  var obj: any = { length: arr.length };
  for (var i: any = 0; i < obj.length; i++) obj[i] = arr[i];
  return obj;
}

var makeIterable: any;
if (typeof Symbol !== "undefined" && Symbol.iterator) {
  makeIterable = function makeIterable(TA: any, primitiveOrIterable: any): any {
    var src: any = makeArray(TA, primitiveOrIterable);
    if (isPrimitive(src)) return src;
    var obj: any = {};
    obj[Symbol.iterator] = function(): any { return src[Symbol.iterator](); };
    return obj;
  };
}

function makeArrayBuffer(TA: any, primitiveOrIterable: any): any {
  var arr: any = makeArray(TA, primitiveOrIterable);
  if (isPrimitive(arr)) return arr;
  return new TA(arr).buffer;
}

var makeResizableArrayBuffer: any, makeGrownArrayBuffer: any, makeShrunkArrayBuffer: any, makeImmutableArrayBuffer: any;
if (ArrayBuffer.prototype.resize) {
  var copyIntoArrayBuffer: any = function(destBuffer: any, srcBuffer: any): any {
    var destView: any = new Uint8Array(destBuffer);
    var srcView: any = new Uint8Array(srcBuffer);
    for (var i: any = 0; i < srcView.length; i++) destView[i] = srcView[i];
    return destBuffer;
  };

  makeResizableArrayBuffer = function makeResizableArrayBuffer(TA: any, primitiveOrIterable: any): any {
    if (isPrimitive(primitiveOrIterable)) {
      var n: any = Number(primitiveOrIterable) * TA.BYTES_PER_ELEMENT;
      if (!(n >= 0 && n < 9007199254740992)) return primitiveOrIterable;
      return new ArrayBuffer(n, { maxByteLength: n * 2 });
    }
    var fixed: any = makeArrayBuffer(TA, primitiveOrIterable);
    var byteLength: any = fixed.byteLength;
    var resizable: any = new ArrayBuffer(byteLength, { maxByteLength: byteLength * 2 });
    return copyIntoArrayBuffer(resizable, fixed);
  };

  makeGrownArrayBuffer = function makeGrownArrayBuffer(TA: any, primitiveOrIterable: any): any {
    if (isPrimitive(primitiveOrIterable)) {
      var n: any = Number(primitiveOrIterable) * TA.BYTES_PER_ELEMENT;
      if (!(n >= 0 && n < 9007199254740992)) return primitiveOrIterable;
      var grown: any = new ArrayBuffer(Math.floor(n / 2), { maxByteLength: n });
      grown.resize(n);
    }
    var fixed: any = makeArrayBuffer(TA, primitiveOrIterable);
    var byteLength: any = fixed.byteLength;
    var grown2: any = new ArrayBuffer(Math.floor(byteLength / 2), { maxByteLength: byteLength });
    grown2.resize(byteLength);
    return copyIntoArrayBuffer(grown2, fixed);
  };

  makeShrunkArrayBuffer = function makeShrunkArrayBuffer(TA: any, primitiveOrIterable: any): any {
    if (isPrimitive(primitiveOrIterable)) {
      var n: any = Number(primitiveOrIterable) * TA.BYTES_PER_ELEMENT;
      if (!(n >= 0 && n < 9007199254740992)) return primitiveOrIterable;
      var shrunk: any = new ArrayBuffer(n * 2, { maxByteLength: n * 2 });
      shrunk.resize(n);
    }
    var fixed: any = makeArrayBuffer(TA, primitiveOrIterable);
    var byteLength: any = fixed.byteLength;
    var shrunk2: any = new ArrayBuffer(byteLength * 2, { maxByteLength: byteLength * 2 });
    copyIntoArrayBuffer(shrunk2, fixed);
    shrunk2.resize(byteLength);
    return shrunk2;
  };
}
if (ArrayBuffer.prototype.transferToImmutable) {
  makeImmutableArrayBuffer = function makeImmutableArrayBuffer(TA: any, primitiveOrIterable: any): any {
    if (isPrimitive(primitiveOrIterable)) {
      var n: any = Number(primitiveOrIterable) * TA.BYTES_PER_ELEMENT;
      if (!(n >= 0 && n < 9007199254740992)) return primitiveOrIterable;
      return (new ArrayBuffer(n)).transferToImmutable();
    }
    var mutable: any = makeArrayBuffer(TA, primitiveOrIterable);
    return mutable.transferToImmutable();
  };
}

var typedArrayCtorArgFactories: any = [makePassthrough, makeArray, makeArrayLike];
if (makeIterable) typedArrayCtorArgFactories.push(makeIterable);
typedArrayCtorArgFactories.push(makeArrayBuffer);
if (makeResizableArrayBuffer) typedArrayCtorArgFactories.push(makeResizableArrayBuffer);
if (makeGrownArrayBuffer) typedArrayCtorArgFactories.push(makeGrownArrayBuffer);
if (makeShrunkArrayBuffer) typedArrayCtorArgFactories.push(makeShrunkArrayBuffer);
if (makeImmutableArrayBuffer) typedArrayCtorArgFactories.push(makeImmutableArrayBuffer);

/**
 * A predicate for testing whether a TypedArray argument factory from this file
 * matches any of the provided features.
 */
function ctorArgFactoryMatchesSome(argFactory: any, features: any): any {
  for (var i: any = 0; i < features.length; ++i) {
    switch (features[i]) {
      case "passthrough":
        if (argFactory === makePassthrough) return true;
        break;
      case "arraylike":
        if (argFactory === makeArray || argFactory === makeArrayLike) return true;
        break;
      case "iterable":
        if (argFactory === makeIterable) return true;
        break;
      case "arraybuffer":
        if (
          argFactory === makeArrayBuffer ||
          argFactory === makeResizableArrayBuffer ||
          argFactory === makeGrownArrayBuffer ||
          argFactory === makeShrunkArrayBuffer ||
          argFactory === makeImmutableArrayBuffer
        ) {
          return true;
        }
        break;
      case "resizable":
        if (
          argFactory === makeResizableArrayBuffer ||
          argFactory === makeGrownArrayBuffer ||
          argFactory === makeShrunkArrayBuffer
        ) {
          return true;
        }
        break;
      case "immutable":
        if (argFactory === makeImmutableArrayBuffer) return true;
        break;
      default:
        throw new Test262Error("unknown feature: " + features[i]);
    }
  }
  return false;
}

/**
 * Calls the provided function with (typedArrayCtor, typedArrayCtorArgFactory)
 * pairs, where typedArrayCtor is Uint8Array/Int8Array/BigInt64Array/etc. and
 * typedArrayCtorArgFactory maps a primitive or iterable into a value suitable
 * as the first argument of typedArrayCtor.
 */
function testWithAllTypedArrayConstructors(f: any, constructors: any, includeArgFactories: any, excludeArgFactories: any): any {
  var ctors: any = constructors || allTypedArrayConstructors;
  var ctorArgFactories: any = typedArrayCtorArgFactories;
  if (includeArgFactories) {
    ctorArgFactories = [];
    for (var i: any = 0; i < typedArrayCtorArgFactories.length; ++i) {
      if (ctorArgFactoryMatchesSome(typedArrayCtorArgFactories[i], includeArgFactories)) {
        ctorArgFactories.push(typedArrayCtorArgFactories[i]);
      }
    }
  }
  if (excludeArgFactories) {
    ctorArgFactories = ctorArgFactories.slice();
    for (var j: any = ctorArgFactories.length - 1; j >= 0; --j) {
      if (ctorArgFactoryMatchesSome(ctorArgFactories[j], excludeArgFactories)) {
        ctorArgFactories.splice(j, 1);
      }
    }
  }
  if (ctorArgFactories.length === 0) {
    throw new Test262Error("no arg factories match include " + includeArgFactories + " and exclude " + excludeArgFactories);
  }
  for (var k: any = 0; k < ctorArgFactories.length; ++k) {
    var argFactory: any = ctorArgFactories[k];
    for (var m: any = 0; m < ctors.length; ++m) {
      var constructor: any = ctors[m];
      var boundArgFactory: any = argFactory.bind(undefined, constructor);
      try {
        f(constructor, boundArgFactory);
      } catch (e) {
        e.message += " (Testing with " + constructor.name + " and " + argFactory.name + ".)";
        throw e;
      }
    }
  }
}

/**
 * Like testWithAllTypedArrayConstructors, defaulting to the non-bigint list.
 */
function testWithTypedArrayConstructors(f: any, constructors: any, includeArgFactories: any, excludeArgFactories: any): any {
  var ctors: any = constructors || typedArrayConstructors;
  testWithAllTypedArrayConstructors(f, ctors, includeArgFactories, excludeArgFactories);
}

/**
 * Calls the provided function for every BigInt typed array constructor.
 */
function testWithBigIntTypedArrayConstructors(f: any, constructors: any, includeArgFactories: any, excludeArgFactories: any): any {
  var ctors: any = constructors || [BigInt64Array, BigUint64Array];
  testWithAllTypedArrayConstructors(f, ctors, includeArgFactories, excludeArgFactories);
}

var nonAtomicsFriendlyTypedArrayConstructors: any = floatArrayConstructors.concat([Uint8ClampedArray]);
/**
 * Calls the provided function for every non-"Atomics Friendly" typed array constructor.
 */
function testWithNonAtomicsFriendlyTypedArrayConstructors(f: any, includeArgFactories: any, excludeArgFactories: any): any {
  testWithAllTypedArrayConstructors(
    f,
    nonAtomicsFriendlyTypedArrayConstructors,
    includeArgFactories,
    excludeArgFactories
  );
}

/**
 * Calls the provided function for every "Atomics Friendly" typed array constructor.
 */
function testWithAtomicsFriendlyTypedArrayConstructors(f: any, includeArgFactories: any, excludeArgFactories: any): any {
  testWithAllTypedArrayConstructors(
    f,
    [
      Int32Array,
      Int16Array,
      Int8Array,
      Uint32Array,
      Uint16Array,
      Uint8Array,
    ],
    includeArgFactories,
    excludeArgFactories
  );
}

/**
 * Helper for conversion operations on TypedArrays, the expected values
 * properties are indexed in order to match the respective value for each
 * TypedArray constructor.
 */
function testTypedArrayConversions(byteConversionValues: any, fn: any): any {
  var values: any = byteConversionValues.values;
  var expected: any = byteConversionValues.expected;

  testWithTypedArrayConstructors(function(TA: any): any {
    var name: any = TA.name.slice(0, -5);

    return values.forEach(function(value: any, index: any): any {
      var exp: any = expected[name][index];
      var initial: any = 0;
      if (exp === 0) {
        initial = 1;
      }
      fn(TA, value, exp, initial);
    });
  }, null, ["passthrough"]);
}

/**
 * Checks if the given argument is one of the float-based TypedArray constructors.
 */
function isFloatTypedArrayConstructor(arg: any): any {
  return floatArrayConstructors.indexOf(arg) !== -1;
}

/**
 * Determines the precision of the given float-based TypedArray constructor.
 */
function floatTypedArrayConstructorPrecision(FA: any): any {
  if (typeof Float16Array !== "undefined" && FA === Float16Array) {
    return "half";
  } else if (FA === Float32Array) {
    return "single";
  } else if (FA === Float64Array) {
    return "double";
  } else {
    throw new Error("Malformed test - floatTypedArrayConstructorPrecision called with non-float TypedArray");
  }
}
