package tc39

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHarnessFingerprintStable pins that the fingerprint is deterministic for an
// unchanged ports tree, so an unchanged harness reuses its results ledger rather
// than starting a fresh one every run.
func TestHarnessFingerprintStable(t *testing.T) {
	dir := t.TempDir()
	writePort(t, dir, "sta.ts", "// sta\n")
	writePort(t, dir, "assert.ts", "// assert\n")

	first, err := HarnessFingerprint(dir)
	if err != nil {
		t.Fatalf("HarnessFingerprint: %v", err)
	}
	second, err := HarnessFingerprint(dir)
	if err != nil {
		t.Fatalf("HarnessFingerprint: %v", err)
	}
	if first != second {
		t.Fatalf("fingerprint not stable: %q vs %q", first, second)
	}
	if first == "" {
		t.Fatal("fingerprint is empty")
	}
}

// TestHarnessFingerprintMovesOnPortEdit pins the whole point of the digest: an
// edit to a ported prelude file moves the fingerprint, so a harness change can
// never replay a stale verdict out of a results cache keyed by the old digest.
func TestHarnessFingerprintMovesOnPortEdit(t *testing.T) {
	dir := t.TempDir()
	writePort(t, dir, "assert.ts", "// assert v1\n")
	before, err := HarnessFingerprint(dir)
	if err != nil {
		t.Fatalf("HarnessFingerprint: %v", err)
	}

	writePort(t, dir, "assert.ts", "// assert v2, a real behavior change\n")
	after, err := HarnessFingerprint(dir)
	if err != nil {
		t.Fatalf("HarnessFingerprint: %v", err)
	}
	if before == after {
		t.Fatalf("fingerprint did not move on a port edit: %q", before)
	}
}

// TestHarnessFingerprintIgnoresNonPortFiles pins that a stray non-.ts file in the
// ports directory does not perturb the digest, so an editor backup or a README
// dropped beside the ports never forces a needless fresh ledger.
func TestHarnessFingerprintIgnoresNonPortFiles(t *testing.T) {
	dir := t.TempDir()
	writePort(t, dir, "assert.ts", "// assert\n")
	before, err := HarnessFingerprint(dir)
	if err != nil {
		t.Fatalf("HarnessFingerprint: %v", err)
	}

	writePort(t, dir, "assert.ts.bak", "// a backup, not a port\n")
	after, err := HarnessFingerprint(dir)
	if err != nil {
		t.Fatalf("HarnessFingerprint: %v", err)
	}
	if before != after {
		t.Fatalf("fingerprint moved on a non-port file: %q vs %q", before, after)
	}
}

func writePort(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
