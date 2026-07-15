// TypeScript port of test262/harness/propertyHelper.js. The upstream file is the
// highest-reach include in the suite: verifyProperty and the verifyEnumerable /
// verifyWritable / verifyConfigurable checks it defines are how a test asserts the
// attributes of a property descriptor. The logic and the assertion messages are
// kept faithful to upstream so a failure reads the same. Two upstream idioms are
// rephrased into forms bento's front door lowers, with no change in behaviour:
// every parameter and the descriptor-shaped locals carry an `any` annotation, so
// the checker admits the JS-as-TS source instead of flagging an implicit any; and
// the receiver-uncurried `Function.prototype.call.bind(Array.prototype.join)`
// captures become arrow wrappers over the same `.call`, which is the same guard
// against a test destroying the primordial method it verifies with, spelled the
// way the lowerer accepts.

// Capture primordial functions and receiver-uncurried primordial methods that
// are used in verification but might be destroyed *by* that process itself.
var __isArray: any = Array.isArray;
var __defineProperty: any = Object.defineProperty;
var __getOwnPropertyDescriptor: any = Object.getOwnPropertyDescriptor;
var __getOwnPropertyNames: any = Object.getOwnPropertyNames;
var __join: any = (thisArg: any, separator: any): any => Array.prototype.join.call(thisArg, separator);
var __push: any = (thisArg: any, element: any): any => Array.prototype.push.call(thisArg, element);
var __hasOwnProperty: any = (thisArg: any, name: any): any => Object.prototype.hasOwnProperty.call(thisArg, name);
var __propertyIsEnumerable: any = (thisArg: any, name: any): any => Object.prototype.propertyIsEnumerable.call(thisArg, name);
var nonIndexNumericPropertyName: any = Math.pow(2, 32) - 1;

function verifyProperty(obj: any, name: any, desc: any, options: any): any {
  assert(
    arguments.length > 2,
    'verifyProperty should receive at least 3 arguments: obj, name, and descriptor'
  );
  var label: any = options && options.label || String(name);

  var originalDesc: any = __getOwnPropertyDescriptor(obj, name);

  // Allows checking for undefined descriptor if it's explicitly given.
  if (desc === undefined) {
    assert.sameValue(
      originalDesc,
      undefined,
      label + " descriptor should be undefined"
    );

    // desc and originalDesc are both undefined, problem solved;
    return true;
  }

  assert(__hasOwnProperty(obj, name), label + " should be an own property");

  assert.notSameValue(
    desc,
    null,
    "The desc argument should be an object or undefined, null"
  );

  assert.sameValue(
    typeof desc,
    "object",
    "The desc argument should be an object or undefined, " + String(desc)
  );

  var names: any = __getOwnPropertyNames(desc);
  for (var i: any = 0; i < names.length; i++) {
    assert(
      names[i] === "value" ||
        names[i] === "writable" ||
        names[i] === "enumerable" ||
        names[i] === "configurable" ||
        names[i] === "get" ||
        names[i] === "set",
      "Invalid descriptor field: " + names[i]
    );
  }

  var failures: any = [];

  if (__hasOwnProperty(desc, 'value')) {
    if (!isSameValue(desc.value, originalDesc.value)) {
      __push(failures, label + " descriptor value should be " + String(desc.value));
    }
    if (!isSameValue(desc.value, obj[name])) {
      __push(failures, label + " value should be " + String(desc.value));
    }
  }

  if (__hasOwnProperty(desc, 'enumerable') && desc.enumerable !== undefined) {
    if (desc.enumerable !== originalDesc.enumerable ||
        desc.enumerable !== isEnumerable(obj, name)) {
      __push(failures, label + " descriptor should " + (desc.enumerable ? '' : 'not ') + "be enumerable");
    }
  }

  // Operations past this point are potentially destructive!

  if (__hasOwnProperty(desc, 'writable') && desc.writable !== undefined) {
    if (desc.writable !== originalDesc.writable ||
        desc.writable !== isWritable(obj, name)) {
      __push(failures, label + " descriptor should " + (desc.writable ? '' : 'not ') + "be writable");
    }
  }

  if (__hasOwnProperty(desc, 'configurable') && desc.configurable !== undefined) {
    if (desc.configurable !== originalDesc.configurable ||
        desc.configurable !== isConfigurable(obj, name)) {
      __push(failures, label + " descriptor should " + (desc.configurable ? '' : 'not ') + "be configurable");
    }
  }

  if (failures.length) {
    assert(false, __join(failures, '; '));
  }

  if (options && options.restore) {
    __defineProperty(obj, name, originalDesc);
  }

  return true;
}

function isConfigurable(obj: any, name: any): any {
  try {
    delete obj[name];
  } catch (e) {
    if (!(e instanceof TypeError)) {
      throw new Test262Error("Expected TypeError, got " + e);
    }
  }
  return !__hasOwnProperty(obj, name);
}

function isEnumerable(obj: any, name: any): any {
  var stringCheck: any = false;

  if (typeof name === "string") {
    for (var x in obj) {
      if (x === name) {
        stringCheck = true;
        break;
      }
    }
  } else {
    // skip it if name is not string, works for Symbol names.
    stringCheck = true;
  }

  return stringCheck && __hasOwnProperty(obj, name) && __propertyIsEnumerable(obj, name);
}

function isSameValue(a: any, b: any): any {
  if (a === 0 && b === 0) return 1 / a === 1 / b;
  if (a !== a && b !== b) return true;

  return a === b;
}

function isWritable(obj: any, name: any, verifyProp: any, value: any): any {
  var unlikelyValue: any = __isArray(obj) && name === "length" ?
    nonIndexNumericPropertyName :
    "unlikelyValue";
  var newValue: any = value || unlikelyValue;
  var hadValue: any = __hasOwnProperty(obj, name);
  var oldValue: any = obj[name];
  var writeSucceeded: any;

  if (arguments.length < 4 && newValue === oldValue) {
    newValue = newValue + "2";
  }

  try {
    obj[name] = newValue;
  } catch (e) {
    if (!(e instanceof TypeError)) {
      throw new Test262Error("Expected TypeError, got " + e);
    }
  }

  writeSucceeded = isSameValue(obj[verifyProp || name], newValue);

  // Revert the change only if it was successful (in other cases, reverting
  // is unnecessary and may trigger exceptions for certain property
  // configurations)
  if (writeSucceeded) {
    if (hadValue) {
      obj[name] = oldValue;
    } else {
      delete obj[name];
    }
  }

  return writeSucceeded;
}

/**
 * Verify that there is a function of specified name, length, and containing
 * descriptor associated with `obj[name]` and following the conventions for
 * built-in objects.
 */
function verifyCallableProperty(obj: any, name: any, functionName: any, functionLength: any, desc: any, options: any): any {
  var label: any = options && options.label || String(name);
  var propertyVerifier: any = options && options.verifyProperty || verifyProperty;

  var value: any = obj && obj[name];

  assert.sameValue(typeof value, "function", label + " should be a function");

  // Every other data property described in clauses 19 through 28 and in
  // Annex B.2 has the attributes { [[Writable]]: true, [[Enumerable]]: false,
  // [[Configurable]]: true } unless otherwise specified.
  if (desc === undefined) {
    desc = {
      writable: true,
      enumerable: false,
      configurable: true,
      value: value
    };
  } else if (!__hasOwnProperty(desc, "value") && !__hasOwnProperty(desc, "get")) {
    desc.value = value;
  }

  propertyVerifier(obj, name, desc, options);

  if (functionName === undefined) {
    if (typeof name === "symbol") {
      functionName = "[" + name.description + "]";
    } else {
      functionName = name;
    }
  }
  // Unless otherwise specified, the "name" property of a built-in function
  // object has the attributes { [[Writable]]: false, [[Enumerable]]: false,
  // [[Configurable]]: true }.
  propertyVerifier(value, "name", {
    value: functionName,
    writable: false,
    enumerable: false,
    configurable: desc.configurable
  }, { label: label + " name", restore: options && options.restore });

  // Unless otherwise specified, the "length" property of a built-in function
  // object has the attributes { [[Writable]]: false, [[Enumerable]]: false,
  // [[Configurable]]: true }.
  propertyVerifier(value, "length", {
    value: functionLength,
    writable: false,
    enumerable: false,
    configurable: desc.configurable
  }, { label: label + " length", restore: options && options.restore });
}

/**
 * Verify that there is an accessor property associated with `obj[name]` and
 * following the conventions for built-in objects.
 */
function verifyAccessorProperty(obj: any, name: any, desc: any, options: any): any {
  var checkGet: any = __hasOwnProperty(desc, "get");
  var checkSet: any = __hasOwnProperty(desc, "set");
  assert(
    checkGet || checkSet,
    'verifyAccessorProperty requires at least one of "get" and "set"'
  );
  var label: any = options && options.label || String(name);
  var propertyVerifier: any = options && options.verifyProperty || verifyProperty;
  var callabilityVerifier: any = options && options.verifyCallableProperty || verifyCallableProperty;

  var originalDesc: any = __getOwnPropertyDescriptor(obj, name);

  if (checkGet) {
    var expectGetter: any = desc.get;
    var getterLabel: any = label + " getter";
    if (expectGetter === undefined || typeof expectGetter === "function") {
      assert.sameValue(originalDesc.get, expectGetter, getterLabel);
    } else {
      var getterName: any = expectGetter.name;
      if (getterName === undefined) {
        getterName = "get " + (typeof name === "symbol" ? "[" + name.description + "]" : name);
      }
      var getterLength: any = expectGetter.length !== undefined ? expectGetter.length : 0;
      var getterOptions: any = { label: getterLabel };
      callabilityVerifier(originalDesc, "get", getterName, getterLength, {}, getterOptions);
    }
  }
  if (checkSet) {
    var expectSetter: any = desc.set;
    var setterLabel: any = label + " setter";
    if (expectSetter === undefined || typeof expectSetter === "function") {
      assert.sameValue(originalDesc.set, expectSetter, setterLabel);
    } else {
      var setterName: any = expectSetter.name;
      if (setterName === undefined) {
        setterName = "set " + (typeof name === "symbol" ? "[" + name.description + "]" : name);
      }
      var setterLength: any = expectSetter.length !== undefined ? expectSetter.length : 1;
      var setterOptions: any = { label: setterLabel };
      callabilityVerifier(originalDesc, "set", setterName, setterLength, {}, setterOptions);
    }
  }

  // Every accessor property described in clauses 19 through 28 and in Annex B.2
  // has the attributes { [[Enumerable]]: false, [[Configurable]]: true } unless
  // otherwise specified.
  var resolvedDesc: any = { get: originalDesc.get, set: originalDesc.set };
  if (!__hasOwnProperty(desc, "enumerable")) {
    resolvedDesc.enumerable = false;
  } else if (desc.enumerable !== undefined) {
    resolvedDesc.enumerable = desc.enumerable;
  }
  if (!__hasOwnProperty(desc, "configurable")) {
    resolvedDesc.configurable = true;
  } else if (desc.configurable !== undefined) {
    resolvedDesc.configurable = desc.configurable;
  }
  propertyVerifier(obj, name, resolvedDesc, options);
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyEqualTo(obj: any, name: any, value: any): any {
  if (!isSameValue(obj[name], value)) {
    throw new Test262Error("Expected obj[" + String(name) + "] to equal " + value +
           ", actually " + obj[name]);
  }
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyWritable(obj: any, name: any, verifyProp: any, value: any): any {
  if (!verifyProp) {
    assert(__getOwnPropertyDescriptor(obj, name).writable,
         "Expected obj[" + String(name) + "] to have writable:true.");
  }
  if (!isWritable(obj, name, verifyProp, value)) {
    throw new Test262Error("Expected obj[" + String(name) + "] to be writable, but was not.");
  }
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyNotWritable(obj: any, name: any, verifyProp: any, value: any): any {
  if (!verifyProp) {
    assert(!__getOwnPropertyDescriptor(obj, name).writable,
         "Expected obj[" + String(name) + "] to have writable:false.");
  }
  if (isWritable(obj, name, verifyProp)) {
    throw new Test262Error("Expected obj[" + String(name) + "] NOT to be writable, but was.");
  }
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyEnumerable(obj: any, name: any): any {
  assert(__getOwnPropertyDescriptor(obj, name).enumerable,
       "Expected obj[" + String(name) + "] to have enumerable:true.");
  if (!isEnumerable(obj, name)) {
    throw new Test262Error("Expected obj[" + String(name) + "] to be enumerable, but was not.");
  }
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyNotEnumerable(obj: any, name: any): any {
  assert(!__getOwnPropertyDescriptor(obj, name).enumerable,
       "Expected obj[" + String(name) + "] to have enumerable:false.");
  if (isEnumerable(obj, name)) {
    throw new Test262Error("Expected obj[" + String(name) + "] NOT to be enumerable, but was.");
  }
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyConfigurable(obj: any, name: any): any {
  assert(__getOwnPropertyDescriptor(obj, name).configurable,
       "Expected obj[" + String(name) + "] to have configurable:true.");
  if (!isConfigurable(obj, name)) {
    throw new Test262Error("Expected obj[" + String(name) + "] to be configurable, but was not.");
  }
}

/**
 * Deprecated; please use `verifyProperty` in new tests.
 */
function verifyNotConfigurable(obj: any, name: any): any {
  assert(!__getOwnPropertyDescriptor(obj, name).configurable,
       "Expected obj[" + String(name) + "] to have configurable:false.");
  if (isConfigurable(obj, name)) {
    throw new Test262Error("Expected obj[" + String(name) + "] NOT to be configurable, but was.");
  }
}

/**
 * Use this function to verify the properties of a primordial object.
 * For non-primordial objects, use verifyProperty.
 */
var verifyPrimordialProperty: any = verifyProperty;

/**
 * Use this function to verify the primordial function-valued properties.
 * For non-primordial functions, use verifyCallableProperty.
 */
function verifyPrimordialCallableProperty(obj: any, name: any, functionName: any, functionLength: any, desc: any, options: any): any {
  var resolvedOptions: any = {
    verifyProperty: options && options.verifyProperty !== undefined
      ? options.verifyProperty
      : verifyPrimordialProperty
  };
  if (options && options.label !== undefined) resolvedOptions.label = options.label;
  if (options && options.restore !== undefined) resolvedOptions.restore = options.restore;

  return verifyCallableProperty(obj, name, functionName, functionLength, desc, resolvedOptions);
}

/**
 * Use this function to verify the primordial accessor properties.
 * For non-primordial functions, use verifyAccessorProperty.
 */
function verifyPrimordialAccessorProperty(obj: any, name: any, desc: any, options: any): any {
  var resolvedOptions: any = {
    verifyProperty: options && options.verifyProperty !== undefined
      ? options.verifyProperty
      : verifyPrimordialProperty,
    verifyCallableProperty: options && options.verifyCallableProperty !== undefined
      ? options.verifyCallableProperty
      : verifyPrimordialCallableProperty
  };
  if (options && options.label !== undefined) resolvedOptions.label = options.label;
  if (options && options.restore !== undefined) resolvedOptions.restore = options.restore;

  return verifyAccessorProperty(obj, name, desc, resolvedOptions);
}
