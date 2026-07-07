// TypeScript port of test262/harness/compareArray.js. Upstream retired that file
// once compareArray moved into assert.js, and this repo's assert.ts carries the
// same compareArray and assert.compareArray, so the symbols a test asks for by
// including compareArray.js are already in scope from the mandatory assert prelude.
// The port exists so the include resolves rather than handing the test back for a
// missing file; it declares nothing of its own on purpose, the way the deprecated
// upstream file adds nothing beyond what assert.js already defines.
