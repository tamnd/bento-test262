package tc39

import (
	"strings"
	"testing"
)

// TestHoistForOfTargets pins the shapes the sloppy for-head hoist wins: a nested
// array pattern, a bare identifier, object shorthand and keyed values, a rest
// element, and a default whose target is hoisted while the default expression's
// own name is not.
func TestHoistForOfTargets(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"nested array for-await", "async function fn(){ for await ([[ x ]] of [[undefined]]) {} }", "var x;\n"},
		{"bare identifier for-of", "for (x of [1]) {}", "var x;\n"},
		{"object shorthand", "for ({ x } of [{}]) {}", "var x;\n"},
		{"object keyed value", "for ({ a: x } of [{}]) {}", "var x;\n"},
		{"array rest", "for ([...x] of [[]]) {}", "var x;\n"},
		{"for-in target", "for (x in {}) {}", "var x;\n"},
		{"two targets", "for ([x, y] of [[]]) {}", "var x, y;\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hoistSloppyForBindings(c.body); got != c.want {
				t.Fatalf("hoistSloppyForBindings(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// TestHoistLeavesReadsAlone pins the safety line: a name is hoisted only when it
// is nothing but a for-head target. A default's referenced name, a name read
// elsewhere, a host probe, a member target, a declared binding, and a C-style
// for init all keep their honest handback.
func TestHoistLeavesReadsAlone(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"target also read", "for ([x] of [[]]) {} log(x);"},                 // x is read after the loop
		{"host probe read", "$262.detachArrayBuffer(buf);"},                  // never a target
		{"member target", "for (a.b of [1]) {}"},                             // member, not a plain name
		{"declared binding", "let x; for ([x] of [[]]) {}"},                  // x is declared
		{"const for binding", "for (const x of [1]) {}"},                     // declaration head, checker is fine
		{"c-style for init", "for (x = 0; x < 3; x++) {}"},                   // not a for-of/in binding
		{"computed key aborts", "for ({ [k]: x } of [{}]) {}"},               // computed key, walk gives up
		{"for in string", "const s = 'for (x of y)';"},                       // masked, not a real head
		{"for in comment", "// for (x of y)\nconst a = 1;"},                  // masked, not a real head
		{"array index write", "const a = []; a[x] = 1;"},                     // index write, no for-head
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hoistSloppyForBindings(c.body); got != "" {
				t.Fatalf("hoistSloppyForBindings(%q) = %q, want no hoist", c.body, got)
			}
		})
	}
}

// TestHoistDefaultTargetOnly pins that in `for ([a = y] of ...)` the target a is
// hoisted while y, read as the default value, is not.
func TestHoistDefaultTargetOnly(t *testing.T) {
	// a is a pure target, y is only ever a read, so a is hoisted and y is not.
	got := hoistSloppyForBindings("var y; for ([a = y] of [[]]) {}")
	if got != "var a;\n" {
		t.Fatalf("hoist = %q, want %q", got, "var a;\n")
	}
}

// TestComposeSloppyHoistsForTarget pins that the sloppy composition of a
// non-negative test declares the loop target the body never declares, placed
// after the ports and before the body.
func TestComposeSloppyHoistsForTarget(t *testing.T) {
	dir := portsDirForTest(t)
	c := Case{Rel: "test/x.js", Source: "for (x of [1]) {}\n"}
	got, err := Compose(dir, c, "sloppy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "var x;\n") {
		t.Fatalf("sloppy compose did not hoist the loop target:\n%s", got)
	}
	hoist := strings.Index(got, "var x;")
	body := strings.Index(got, "for (x of [1])")
	ports := strings.Index(got, "class Test262Error")
	if !(ports < hoist && hoist < body) {
		t.Fatalf("hoist must sit after the ports and before the body:\n%s", got)
	}
}

// TestComposeStrictDoesNotHoist pins that strict mode keeps the name error: a
// strict test wants the undeclared name to fail the checker.
func TestComposeStrictDoesNotHoist(t *testing.T) {
	dir := portsDirForTest(t)
	c := Case{Rel: "test/x.js", Source: "for (x of [1]) {}\n"}
	got, err := Compose(dir, c, "strict")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "var x;") {
		t.Fatalf("strict compose must not hoist:\n%s", got)
	}
}

// TestComposeNegativeDoesNotHoist pins that a negative test is never hoisted,
// since it wants its failure and a hoist could mask it.
func TestComposeNegativeDoesNotHoist(t *testing.T) {
	dir := portsDirForTest(t)
	c := Case{
		Rel:    "test/x.js",
		Source: "for (x of [1]) {}\n",
		Meta:   Meta{Negative: &Negative{Phase: "runtime", Type: "ReferenceError"}},
	}
	got, err := Compose(dir, c, "sloppy")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "var x;") {
		t.Fatalf("negative test must not be hoisted:\n%s", got)
	}
}
