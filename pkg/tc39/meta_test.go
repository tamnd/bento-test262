package tc39

import "testing"

func TestParseMeta(t *testing.T) {
	src := `// Copyright notice.
/*---
esid: sec-addition-operator-plus
description: Sample test
includes: [compareArray.js]
flags: [onlyStrict, async]
features: [Symbol.iterator]
negative:
  phase: parse
  type: SyntaxError
---*/
throw "never parsed";
`
	m, err := ParseMeta(src)
	if err != nil {
		t.Fatal(err)
	}
	if m.Esid != "sec-addition-operator-plus" {
		t.Errorf("esid = %q", m.Esid)
	}
	if len(m.Includes) != 1 || m.Includes[0] != "compareArray.js" {
		t.Errorf("includes = %v", m.Includes)
	}
	if !m.HasFlag("onlyStrict") || !m.HasFlag("async") || m.HasFlag("raw") {
		t.Errorf("flags = %v", m.Flags)
	}
	if !m.HasFeature("Symbol.iterator") {
		t.Errorf("features = %v", m.Features)
	}
	if m.Negative == nil || m.Negative.Phase != "parse" || m.Negative.Type != "SyntaxError" {
		t.Errorf("negative = %+v", m.Negative)
	}
}

func TestParseMetaMissingBlock(t *testing.T) {
	m, err := ParseMeta("var x = 1;")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Flags) != 0 || m.Negative != nil {
		t.Errorf("expected zero meta, got %+v", m)
	}
}

func TestModesOf(t *testing.T) {
	cases := []struct {
		flags []string
		want  []string
	}{
		{nil, []string{"sloppy", "strict"}},
		{[]string{"onlyStrict"}, []string{"strict"}},
		{[]string{"noStrict"}, []string{"sloppy"}},
		{[]string{"module"}, []string{"module"}},
		{[]string{"raw"}, []string{"raw"}},
	}
	for _, c := range cases {
		got, err := modesOf(Meta{Flags: c.flags})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("modesOf(%v) = %v, want %v", c.flags, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("modesOf(%v) = %v, want %v", c.flags, got, c.want)
			}
		}
	}
}

// TestExecuteAOTVerdicts drives the real pipeline end to end: stage the bento
// module, lower, compile, run, judge. It needs the Go toolchain and a few
// seconds of build cache warming, so -short skips it.
func TestExecuteAOTVerdicts(t *testing.T) {
	if testing.Short() {
		t.Skip("stages the bento module and runs the toolchain")
	}
	root, _, err := PrepareModuleRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const timeout = 30 * 1e9

	pass := ExecuteAOT(Job{ID: "a", Name: "a.ts", Source: "let n: number = 2;\nif (n !== 2) { throw new Error('no'); }\nconsole.log(n);"}, root, timeout, nil, "")
	if pass.Status != "pass" {
		t.Errorf("plain pass: %+v", pass)
	}
	fail := ExecuteAOT(Job{ID: "b", Name: "b.ts", Source: "throw new Error('boom');"}, root, timeout, nil, "")
	if fail.Status != "fail" && fail.Status != "handback" {
		t.Errorf("thrown error must not pass: %+v", fail)
	}
	neg := ExecuteAOT(Job{ID: "c", Name: "c.ts", Source: "let x = 1 +;", NegType: "SyntaxError", NegPhase: "parse"}, root, timeout, nil, "")
	if neg.Status != "pass" {
		t.Errorf("negative parse should pass when the build rejects: %+v", neg)
	}
	negMiss := ExecuteAOT(Job{ID: "d", Name: "d.ts", Source: "let ok: number = 1;\nconsole.log(ok);", NegType: "SyntaxError", NegPhase: "parse"}, root, timeout, nil, "")
	if negMiss.Status != "fail" {
		t.Errorf("negative with no error must fail: %+v", negMiss)
	}
}
