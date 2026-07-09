package tc39

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// The results cache is what keeps reruns and CI fast: a job's outcome is a
// pure function of the composed source, the bento version, and the judge, so
// a job whose key was seen before returns its recorded result without
// touching the compiler or the toolchain. Bumping bento changes every key,
// which is correct, since a new compiler can change any outcome. Timeouts and
// crashes are never cached; they are the two statuses a machine can cause.

// cacheEntry is one line of the cache file.
type cacheEntry struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Cache maps job keys to recorded results.
type Cache struct {
	path    string
	entries map[string]cacheEntry
	added   int
	// stream appends each new entry to the file as it is recorded, so a run that
	// is killed partway keeps every job it already finished and the next run
	// resumes from there instead of starting over. It is nil until BeginStream
	// opens it. The final Save compacts the appended lines back to one per key.
	stream *bufio.Writer
	file   *os.File
}

// JobKey identifies a job execution for caching: the exact composed source,
// the judge-relevant metadata, and the bento version it ran against.
func JobKey(j Job, bentoVersion string) string {
	h := sha256.New()
	h.Write([]byte(bentoVersion))
	h.Write([]byte{0})
	h.Write([]byte(j.ID))
	h.Write([]byte{0})
	h.Write([]byte(j.NegType))
	h.Write([]byte{0})
	h.Write([]byte(j.NegPhase))
	h.Write([]byte{0})
	if j.Async {
		h.Write([]byte{1})
	}
	h.Write([]byte{0})
	h.Write([]byte(j.Source))
	return hex.EncodeToString(h.Sum(nil))
}

// LoadCache reads the cache file; a missing file is an empty cache.
func LoadCache(path string) (*Cache, error) {
	c := &Cache{path: path, entries: map[string]cacheEntry{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e cacheEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Key != "" {
			c.entries[e.Key] = e
		}
	}
	return c, sc.Err()
}

// BeginStream opens the cache file for append so every Put is flushed to disk as
// it happens, which is what makes an interrupted run resumable: the completed
// jobs are already on disk, and the next run loads them and short-circuits. The
// appended lines may repeat a key across runs, which LoadCache tolerates (last
// line wins) and the final Save compacts. A cache without a stream keeps the old
// behavior of holding results in memory until Save.
func (c *Cache) BeginStream() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	c.file = f
	c.stream = bufio.NewWriter(f)
	return nil
}

// CloseStream flushes and closes the append stream. It is safe to call when no
// stream was opened. The caller runs it before Save so the compacting rewrite
// does not race the append handle.
func (c *Cache) CloseStream() error {
	if c.stream == nil {
		return nil
	}
	err := c.stream.Flush()
	if cerr := c.file.Close(); err == nil {
		err = cerr
	}
	c.stream = nil
	c.file = nil
	return err
}

// Get returns the cached result for a key, rebound to the job's ID.
func (c *Cache) Get(key, id string) (Result, bool) {
	e, ok := c.entries[key]
	if !ok {
		return Result{}, false
	}
	return Result{ID: id, Status: e.Status, Error: e.Error}, true
}

// Put records a result under a key. Nondeterministic statuses stay out.
func (c *Cache) Put(key string, r Result) {
	if r.Status == "timeout" || r.Status == "crash" {
		return
	}
	if _, ok := c.entries[key]; ok {
		return
	}
	e := cacheEntry{Key: key, Status: r.Status, Error: r.Error}
	c.entries[key] = e
	c.added++
	// Flush the entry the moment it is recorded when streaming, so a run killed
	// mid-flight loses nothing it already finished. A go build dwarfs a single
	// flushed line, so paying it per result costs nothing next to what it saves.
	if c.stream != nil {
		if b, err := json.Marshal(e); err == nil {
			_, _ = c.stream.Write(append(b, '\n'))
			_ = c.stream.Flush()
		}
	}
}

// Save writes the cache back when this run added anything, compacting the
// appended stream to one line per key. It closes the append stream first so the
// atomic rename does not race an open handle.
func (c *Cache) Save() error {
	if err := c.CloseStream(); err != nil {
		return err
	}
	if c.added == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range c.entries {
		if err := enc.Encode(e); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
