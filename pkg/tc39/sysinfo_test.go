package tc39

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{512, "512B"},
		{1024, "1.0KB"},
		{6 << 20, "6.0MB"},
		{24 * (1 << 30), "24.0GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPerWorkerMemBytesEnv(t *testing.T) {
	if got := perWorkerMemBytes(); got != 6144<<20 {
		t.Fatalf("default per-worker = %d, want %d", got, 6144<<20)
	}
	t.Setenv("BENTO262_MEM_PER_WORKER_MB", "1000")
	if got := perWorkerMemBytes(); got != 1000<<20 {
		t.Fatalf("override per-worker = %d, want %d", got, 1000<<20)
	}
	// A junk override falls back to the default rather than zeroing the divisor.
	t.Setenv("BENTO262_MEM_PER_WORKER_MB", "nope")
	if got := perWorkerMemBytes(); got != 6144<<20 {
		t.Fatalf("junk override should fall back to default, got %d", got)
	}
}

func TestMemFractionEnv(t *testing.T) {
	if got := memFraction(); got != 0.5 {
		t.Fatalf("default fraction = %v, want 0.5", got)
	}
	t.Setenv("BENTO262_MEM_FRACTION", "0.25")
	if got := memFraction(); got != 0.25 {
		t.Fatalf("override fraction = %v, want 0.25", got)
	}
	// Out-of-range values are ignored so the fraction stays in (0,1].
	for _, bad := range []string{"0", "1.5", "-1", "x"} {
		t.Setenv("BENTO262_MEM_FRACTION", bad)
		if got := memFraction(); got != 0.5 {
			t.Fatalf("fraction %q should fall back to default, got %v", bad, got)
		}
	}
}

func TestCapWorkersNeverRaisesAndFloorsAtOne(t *testing.T) {
	// A request of one is always honored: the cap only reduces, never raises.
	if capped, reason := CapWorkers(1); capped != 1 || reason != "" {
		t.Fatalf("CapWorkers(1) = (%d, %q), want (1, \"\")", capped, reason)
	}
	// On a machine that reports memory, a huge request is bounded to the
	// memory-safe count and a reason is given; where memory is unknown it passes
	// through unchanged. Either way the result never exceeds the request and is
	// at least one.
	capped, reason := CapWorkers(1 << 20)
	if capped < 1 || capped > 1<<20 {
		t.Fatalf("CapWorkers(1<<20) = %d, out of range", capped)
	}
	if safe, total := MemSafeWorkers(); total != 0 {
		if capped != safe {
			t.Fatalf("capped = %d, want memory-safe %d", capped, safe)
		}
		if reason == "" {
			t.Fatal("expected a reason when the request exceeds the memory-safe count")
		}
	}
}
