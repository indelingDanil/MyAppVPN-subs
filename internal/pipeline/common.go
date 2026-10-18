// Package pipeline implements the collect → validate → publish stages. All stages read and
// write plain files inside a work directory so each one can be run (and inspected) alone:
//
//	<dir>/state/sources.json   per-source statistics (overlay on registry/sources.json)
//	<dir>/state/history.json   per-config liveness history
//	<dir>/candidates.jsonl     collect output
//	<dir>/alive.jsonl          validate output
//	<dir>/out/                 publish output (contents of the `output` branch)
package pipeline

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Candidate is one deduplicated, app-acceptable config.
type Candidate struct {
	Key       string   `json:"key"`
	URI       string   `json:"uri"` // link without #remark
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Security  string   `json:"security"`
	Transport string   `json:"transport"`
	Sources   []string `json:"sources"` // up to 3 source URLs, best first
	Pinned    bool     `json:"pinned,omitempty"`

	// filled by validate
	IP      string `json:"ip,omitempty"`       // address the TCP probe connected to
	TCPms   int    `json:"tcp_ms,omitempty"`   // TCP connect time
	DelayMs int    `json:"delay_ms,omitempty"` // xray-knife real delay (0 when skipped)
	ExitIP  string `json:"exit_ip,omitempty"`  // egress IP reported by xray-knife, if any
	ExitCC  string `json:"exit_cc,omitempty"`  // egress country reported by xray-knife, if any
}

// Paths.
func StateDir(dir string) string         { return filepath.Join(dir, "state") }
func SourcesStatePath(dir string) string { return filepath.Join(StateDir(dir), "sources.json") }
func HistoryPath(dir string) string      { return filepath.Join(StateDir(dir), "history.json") }
func CandidatesPath(dir string) string   { return filepath.Join(dir, "candidates.jsonl") }
func AlivePath(dir string) string        { return filepath.Join(dir, "alive.jsonl") }

func writeJSONL(path string, items []Candidate) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for i := range items {
		if err := enc.Encode(&items[i]); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readJSONL(path string) ([]Candidate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Candidate
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var c Candidate
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// HistoryEntry tracks how stable a config is across runs.
type HistoryEntry struct {
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	LastOK    time.Time `json:"last_ok,omitempty"`
	OKStreak  int       `json:"ok_streak"`
}

// History is keyed by Candidate.Key.
type History map[string]*HistoryEntry

// HistoryKeepDays drops entries not seen in any source for this long.
const HistoryKeepDays = 14

func LoadHistory(path string) (History, error) {
	h := History{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return h, nil
	}
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	return h, nil
}

func (h History) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(h) // compact: this file can hold ~20k keys
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Observe records one validation result.
func (h History) Observe(key string, ok bool, now time.Time) {
	e := h[key]
	if e == nil {
		e = &HistoryEntry{FirstSeen: now}
		h[key] = e
	}
	e.LastSeen = now
	if ok {
		e.OKStreak++
		e.LastOK = now
	} else {
		e.OKStreak = 0
	}
}

// Expire removes entries not seen for HistoryKeepDays.
func (h History) Expire(now time.Time) {
	for k, e := range h {
		if now.Sub(e.LastSeen) > HistoryKeepDays*24*time.Hour {
			delete(h, k)
		}
	}
}
