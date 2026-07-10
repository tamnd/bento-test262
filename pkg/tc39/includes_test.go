package tc39

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writePorts creates a ports directory holding a .ts file for each given include
// name, the shape AuditIncludes and Compose look up.
func writePorts(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		port := filepath.Join(dir, name)
		if err := os.WriteFile(port, []byte("// port\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestAuditIncludesSplitsPortedAndUnported pins that the audit sorts each
// referenced include into the ported or unported list by whether a .ts port
// exists, and that it always counts the mandatory sta.js and assert.js the
// preludes carry.
func TestAuditIncludesSplitsPortedAndUnported(t *testing.T) {
	ports := writePorts(t, "sta.ts", "assert.ts", "compareArray.ts")
	cases := []Case{
		{Rel: "a.js", Meta: Meta{Includes: []string{"compareArray.js"}}},
		{Rel: "b.js", Meta: Meta{Includes: []string{"propertyHelper.js"}}},
	}
	report, err := AuditIncludes(ports, cases)
	if err != nil {
		t.Fatal(err)
	}
	ported := map[string]bool{}
	for _, s := range report.Ported {
		ported[s.Name] = true
	}
	for _, want := range []string{"sta.js", "assert.js", "compareArray.js"} {
		if !ported[want] {
			t.Errorf("%s should be ported", want)
		}
	}
	if len(report.Unported) != 1 || report.Unported[0].Name != "propertyHelper.js" {
		t.Fatalf("unported = %+v, want only propertyHelper.js", report.Unported)
	}
}

// TestAuditIncludesJobReach pins that an include's reach weighs each referencing
// file by its mode count: a default test runs sloppy and strict, so it counts two
// jobs, and an onlyStrict test counts one.
func TestAuditIncludesJobReach(t *testing.T) {
	ports := writePorts(t, "sta.ts", "assert.ts")
	cases := []Case{
		// Default flags: two modes, so two jobs.
		{Rel: "a.js", Meta: Meta{Includes: []string{"propertyHelper.js"}}},
		// onlyStrict: one mode, so one job.
		{Rel: "b.js", Meta: Meta{Includes: []string{"propertyHelper.js"}, Flags: []string{"onlyStrict"}}},
	}
	report, err := AuditIncludes(ports, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unported) != 1 {
		t.Fatalf("unported = %+v, want one entry", report.Unported)
	}
	got := report.Unported[0]
	if got.Jobs != 3 {
		t.Errorf("propertyHelper.js jobs = %d, want 3 (2 modes + 1 mode)", got.Jobs)
	}
	if got.Files != 2 {
		t.Errorf("propertyHelper.js files = %d, want 2", got.Files)
	}
}

// TestAuditIncludesAsyncCountsDonePrint pins that an async test pulls in
// doneprintHandle.js exactly as Compose does, so the audit's ported/unported view
// matches what the run actually loads.
func TestAuditIncludesAsyncCountsDonePrint(t *testing.T) {
	ports := writePorts(t, "sta.ts", "assert.ts", "doneprintHandle.ts")
	cases := []Case{
		{Rel: "a.js", Meta: Meta{Flags: []string{"async"}}},
	}
	report, err := AuditIncludes(ports, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unported) != 0 {
		t.Fatalf("unported = %+v, want none", report.Unported)
	}
	names := map[string]bool{}
	for _, s := range report.Ported {
		names[s.Name] = true
	}
	if !names["doneprintHandle.js"] {
		t.Errorf("async test should reference doneprintHandle.js, got %v", names)
	}
}

// TestAuditIncludesDedupesPerFile pins that a file naming the same include twice
// counts it once, matching Compose's dedup so the reach is not double-weighted.
func TestAuditIncludesDedupesPerFile(t *testing.T) {
	ports := writePorts(t, "sta.ts", "assert.ts")
	cases := []Case{
		{Rel: "a.js", Meta: Meta{Includes: []string{"propertyHelper.js", "propertyHelper.js"}, Flags: []string{"onlyStrict"}}},
	}
	report, err := AuditIncludes(ports, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unported) != 1 || report.Unported[0].Files != 1 || report.Unported[0].Jobs != 1 {
		t.Fatalf("unported = %+v, want propertyHelper.js counted once", report.Unported)
	}
}

// TestAuditIncludesMirrorsCompose pins the audit against Compose itself: for every
// name the audit calls ported, Compose loads the case without an UnportedInclude,
// and for an unported name Compose hands back. This is the invariant that makes
// the report authoritative rather than a separate guess at the same question.
func TestAuditIncludesMirrorsCompose(t *testing.T) {
	ports := writePorts(t, "sta.ts", "assert.ts")
	ported := Case{Rel: "ok.js", Source: "1;\n", Meta: Meta{Flags: []string{"onlyStrict"}}}
	unported := Case{Rel: "gap.js", Source: "1;\n", Meta: Meta{Includes: []string{"propertyHelper.js"}, Flags: []string{"onlyStrict"}}}

	report, err := AuditIncludes(ports, []Case{ported, unported})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unported) != 1 || report.Unported[0].Name != "propertyHelper.js" {
		t.Fatalf("audit unported = %+v, want propertyHelper.js", report.Unported)
	}

	if _, err := Compose(ports, ported, "strict"); err != nil {
		t.Errorf("Compose on the all-ported case errored: %v", err)
	}
	_, err = Compose(ports, unported, "strict")
	var miss *UnportedInclude
	if !errors.As(err, &miss) || miss.Name != "propertyHelper.js" {
		t.Errorf("Compose on the unported case = %v, want UnportedInclude for propertyHelper.js", err)
	}
}
