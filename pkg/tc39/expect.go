package tc39

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Expectations is the committed snapshot of every job that does not pass,
// mapping job ID to its recorded status. A job absent from the snapshot is
// expected to pass. The suite is green when reality matches the snapshot
// exactly; any drift, in either direction, is reported and recorded on
// purpose with the update flag, the same discipline as a golden file.
type Expectations map[string]string

// LoadExpectations reads "<status> <job id>" lines, ignoring blanks and #
// comments. A missing file expects everything to pass, which is where this
// ends up when the work is done.
func LoadExpectations(path string) (Expectations, error) {
	out := Expectations{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		status, id, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		out[id] = status
	}
	return out, nil
}

// WriteExpectations records every non-passing result, sorted by job ID.
func WriteExpectations(path string, results map[string]Result) error {
	var ids []string
	for id, r := range results {
		if r.Status != "pass" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("# Non-passing jobs, one \"<status> <job id>\" per line.\n")
	b.WriteString("# Regenerate with bento262 run -update.\n")
	for _, id := range ids {
		b.WriteString(results[id].Status)
		b.WriteByte(' ')
		b.WriteString(id)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// Change is one job whose status moved away from the snapshot.
type Change struct {
	ID   string
	Want string
	Got  string
}

// Diff compares a run against the snapshot. Improvements are jobs that now
// pass; everything else that moved is a regression or a reclassification the
// snapshot has to be updated for.
func Diff(results map[string]Result, exp Expectations) (regressions, improvements []Change) {
	for id, r := range results {
		want, listed := exp[id]
		if !listed {
			want = "pass"
		}
		if r.Status == want {
			continue
		}
		c := Change{ID: id, Want: want, Got: r.Status}
		if r.Status == "pass" {
			improvements = append(improvements, c)
		} else {
			regressions = append(regressions, c)
		}
	}
	sort.Slice(regressions, func(i, j int) bool { return regressions[i].ID < regressions[j].ID })
	sort.Slice(improvements, func(i, j int) bool { return improvements[i].ID < improvements[j].ID })
	return regressions, improvements
}

type tally struct{ pass, fail, handback, crash, timeout int }

// Summarize prints per-area counts, area being the path down to the second
// directory level, which matches how test262 groups its material.
func Summarize(w *os.File, results map[string]Result) {
	areas := map[string]*tally{}
	total := tally{}
	for id, r := range results {
		area := areaOf(id)
		t := areas[area]
		if t == nil {
			t = &tally{}
			areas[area] = t
		}
		bump(t, r.Status)
		bump(&total, r.Status)
	}
	var names []string
	for a := range areas {
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		printTally(w, a, areas[a])
	}
	printTally(w, "TOTAL", &total)
}

func printTally(w *os.File, name string, t *tally) {
	n := t.pass + t.fail + t.handback + t.crash + t.timeout
	if n == 0 {
		return
	}
	fmt.Fprintf(w, "%-55s %6d pass / %6d run  (%5.1f%%)  handback %d fail %d crash %d timeout %d\n",
		name, t.pass, n, 100*float64(t.pass)/float64(n), t.handback, t.fail, t.crash, t.timeout)
}

func bump(t *tally, status string) {
	switch status {
	case "pass":
		t.pass++
	case "fail":
		t.fail++
	case "handback":
		t.handback++
	case "timeout":
		t.timeout++
	default:
		t.crash++
	}
}

// ReasonCount is one recorded error string and how many jobs share it.
type ReasonCount struct {
	Reason string
	Count  int
}

// TopReasons groups the results of one status by their error string and
// returns the n biggest groups. For handbacks this is the work order: the top
// reason is the lowering gap whose fix moves the most jobs.
func TopReasons(results map[string]Result, status string, n int) []ReasonCount {
	counts := map[string]int{}
	for _, r := range results {
		if r.Status != status {
			continue
		}
		reason := r.Error
		if reason == "" {
			reason = "(no error recorded)"
		}
		counts[reason]++
	}
	out := make([]ReasonCount, 0, len(counts))
	for reason, c := range counts {
		out = append(out, ReasonCount{Reason: reason, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// PrintReasons writes the top reasons for a status, biggest first.
func PrintReasons(w *os.File, results map[string]Result, status string, n int) {
	top := TopReasons(results, status, n)
	if len(top) == 0 {
		return
	}
	fmt.Fprintf(w, "\ntop %s reasons:\n", status)
	for _, rc := range top {
		fmt.Fprintf(w, "%8d  %s\n", rc.Count, rc.Reason)
	}
}

// areaOf reduces a job ID to its reporting bucket: test/<kind>/<group>.
func areaOf(id string) string {
	parts := strings.SplitN(id, "/", 4)
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "/")
	}
	return id
}
