// TypeScript port of test262/harness/regExpUtils.js. These helpers assert the
// correctness of RegExp objects: buildString stitches a source string from lone
// code points and ranges, testPropertyEscapes / testPropertyOfStrings drive a
// pattern against matching and non-matching input, and matchValidator returns a
// closure that checks an exec result. The logic and the assertion messages are
// kept faithful to upstream; the only change for the front door is the `any`
// annotation on every parameter and the local that holds a mutating length, so
// the checker admits the JS-as-TS source instead of flagging an implicit any.
// String.fromCodePoint.apply, a chained `arr.length = n = 0`, codePointAt,
// padStart, and for-of over a string all lower as written.

function buildString(args: any): any {
  // Use member expressions rather than destructuring `args` for improved
  // compatibility with engines that only implement assignment patterns
  // partially or not at all.
  const loneCodePoints: any = args.loneCodePoints;
  const ranges: any = args.ranges;
  const CHUNK_SIZE: any = 10000;
  let result: any = String.fromCodePoint.apply(null, loneCodePoints);
  for (let i: any = 0; i < ranges.length; i++) {
    let range: any = ranges[i];
    let start: any = range[0];
    let end: any = range[1];
    let codePoints: any = [];
    for (let length: any = 0, codePoint: any = start; codePoint <= end; codePoint++) {
      codePoints[length++] = codePoint;
      if (length === CHUNK_SIZE) {
        result += String.fromCodePoint.apply(null, codePoints);
        codePoints.length = length = 0;
      }
    }
    result += String.fromCodePoint.apply(null, codePoints);
  }
  return result;
}

function printCodePoint(codePoint: any): any {
  const hex: any = codePoint
    .toString(16)
    .toUpperCase()
    .padStart(6, "0");
  return `U+${hex}`;
}

function printStringCodePoints(string: any): any {
  const buf: any = [];
  for (let symbol of string) {
    let formatted: any = printCodePoint(symbol.codePointAt(0));
    buf.push(formatted);
  }
  return buf.join(' ');
}

function testPropertyEscapes(regExp: any, string: any, expression: any): any {
  if (!regExp.test(string)) {
    for (let symbol of string) {
      let formatted: any = printCodePoint(symbol.codePointAt(0));
      assert(
        regExp.test(symbol),
        `\`${ expression }\` should match ${ formatted } (\`${ symbol }\`)`
      );
    }
  }
}

function testPropertyOfStrings(args: any): any {
  // Use member expressions rather than destructuring `args` for improved
  // compatibility with engines that only implement assignment patterns
  // partially or not at all.
  const regExp: any = args.regExp;
  const expression: any = args.expression;
  const matchStrings: any = args.matchStrings;
  const nonMatchStrings: any = args.nonMatchStrings;
  const allStrings: any = matchStrings.join('');
  if (!regExp.test(allStrings)) {
    for (let string of matchStrings) {
      assert(
        regExp.test(string),
        `\`${ expression }\` should match ${ string } (${ printStringCodePoints(string) })`
      );
    }
  }

  if (!nonMatchStrings) return;

  const allNonMatchStrings: any = nonMatchStrings.join('');
  if (regExp.test(allNonMatchStrings)) {
    for (let string of nonMatchStrings) {
      assert(
        !regExp.test(string),
        `\`${ expression }\` should not match ${ string } (${ printStringCodePoints(string) })`
      );
    }
  }
}

// The exact same logic can be used to test extended character classes
// as enabled through the RegExp `v` flag. This is useful to test not
// just standalone properties of strings, but also string literals, and
// set operations.
const testExtendedCharacterClass: any = testPropertyOfStrings;

// Returns a function that validates a RegExp match result.
//
// Example:
//
//    var validate = matchValidator(['b'], 1, 'abc');
//    validate(/b/.exec('abc'));
//
function matchValidator(expectedEntries: any, expectedIndex: any, expectedInput: any): any {
  return function(match: any): any {
    assert.compareArray(match, expectedEntries, 'Match entries');
    assert.sameValue(match.index, expectedIndex, 'Match index');
    assert.sameValue(match.input, expectedInput, 'Match input');
  }
}
