// Package registry keeps the list of subscription sources (registry/sources.json), the
// always-included pinned list and the blocklist, plus the pruning / scoring rules.
package registry

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source is one subscription URL and its health statistics.
type Source struct {
	URL        string    `json:"url"`
	OriginRepo string    `json:"origin_repo,omitempty"`
	Added      time.Time `json:"added"`
	LastFetch  time.Time `json:"last_fetch,omitempty"`
	LastOK     time.Time `json:"last_ok,omitempty"`
	FailStreak int       `json:"fail_streak"`
	ZeroStreak int       `json:"zero_streak"` // consecutive successful fetches with yield_valid == 0
	YieldTotal int       `json:"yield_total"` // vless:// links in the last successful fetch
	YieldValid int       `json:"yield_valid"` // of those, passing the app filter (deduped within source)
	YieldAlive int       `json:"yield_alive"` // of those, alive in the last validate run
	Score      float64   `json:"score"`
	Pinned     bool      `json:"pinned"`

	// Availability / freshness (see freshness.go).
	DeadSince        time.Time `json:"dead_since,omitempty"`       // first 404/410/451 of the current outage
	DeadStatus       int       `json:"dead_status,omitempty"`      // that HTTP status
	LastUpdated      time.Time `json:"last_updated,omitempty"`     // when the content last changed (per freshness_method)
	FreshnessMethod  string    `json:"freshness_method,omitempty"` // github | last-modified | hash
	Stale            bool      `json:"stale"`
	ContentHash      string    `json:"content_hash,omitempty"`       // sha256 of the last 200 body
	ContentChangedAt time.Time `json:"content_changed_at,omitempty"` // last time ContentHash changed
	ObservedSince    time.Time `json:"observed_since,omitempty"`     // first successful fetch
}

// Limits.
const (
	MaxSources     = 300
	PruneFails     = 5
	PruneZeroRuns  = 3
	PruneStaleDays = 30
)

// Registry is the in-memory registry.
type Registry struct {
	Sources   []*Source
	blocklist []string
}

// Paths inside the registry directory.
func sourcesPath(dir string) string { return filepath.Join(dir, "sources.json") }
func pinnedPath(dir string) string  { return filepath.Join(dir, "pinned.txt") }
func blockPath(dir string) string   { return filepath.Join(dir, "blocklist.txt") }

// Load reads <dir>/sources.json, <dir>/pinned.txt and <dir>/blocklist.txt. If statePath
// points to a newer copy of the statistics (kept on the `output` branch by collect), its
// numbers are overlaid onto matching URLs. Pinned URLs are always present and pinned;
// blocklisted URLs are dropped.
func Load(dir, statePath string, now time.Time) (*Registry, error) {
	r := &Registry{}
	var err error
	if r.blocklist, err = readLines(blockPath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var base []*Source
	if err := readJSON(sourcesPath(dir), &base); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var state []*Source
	if statePath != "" {
		if err := readJSON(statePath, &state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	byURL := map[string]*Source{}
	for _, s := range state {
		if n, ok := NormalizeURL(s.URL); ok {
			byURL[n] = s
		}
	}
	seen := map[string]bool{}
	add := func(s *Source) {
		n, ok := NormalizeURL(s.URL)
		if !ok || seen[n] || r.Blocked(n) {
			return
		}
		s.URL = n
		if st := byURL[n]; st != nil && !st.LastFetch.Before(s.LastFetch) {
			pinned, origin, added := s.Pinned, s.OriginRepo, s.Added
			*s = *st
			s.URL, s.Pinned, s.OriginRepo, s.Added = n, pinned, origin, added
		}
		seen[n] = true
		r.Sources = append(r.Sources, s)
	}
	for _, s := range base {
		s.Pinned = false // pinned.txt is the single source of truth
		add(s)
	}
	pins, err := readLines(pinnedPath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pinSet := map[string]bool{}
	for _, p := range pins {
		if n, ok := NormalizeURL(p); ok {
			pinSet[n] = true
		}
	}
	for _, s := range r.Sources {
		s.Pinned = pinSet[s.URL]
	}
	for _, p := range pins {
		if n, ok := NormalizeURL(p); ok && !seen[n] {
			add(&Source{URL: n, Added: now, Pinned: true})
		}
	}
	return r, nil
}

// Save writes sources.json (sorted: pinned first, then score desc, then URL).
func (r *Registry) Save(path string) error {
	r.sort()
	b, err := json.MarshalIndent(r.Sources, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// SaveTo writes <dir>/sources.json.
func (r *Registry) SaveTo(dir string) error { return r.Save(sourcesPath(dir)) }

func (r *Registry) sort() {
	sort.SliceStable(r.Sources, func(i, j int) bool {
		a, b := r.Sources[i], r.Sources[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.URL < b.URL
	})
}

// Ordered returns sources in priority order (pinned, then score).
func (r *Registry) Ordered() []*Source {
	r.sort()
	return r.Sources
}

// Has reports whether url (normalized) is already registered.
func (r *Registry) Has(u string) bool {
	n, ok := NormalizeURL(u)
	if !ok {
		return false
	}
	for _, s := range r.Sources {
		if s.URL == n {
			return true
		}
	}
	return false
}

// Add registers a new source (no-op if known or blocked). Returns true when added.
func (r *Registry) Add(u, origin string, now time.Time, score float64) bool {
	n, ok := NormalizeURL(u)
	if !ok || r.Has(n) || r.Blocked(n) {
		return false
	}
	r.Sources = append(r.Sources, &Source{URL: n, OriginRepo: origin, Added: now, Score: score})
	return true
}

// Blocked reports whether a URL matches the blocklist. Entries without "/" match the host
// and its subdomains; entries with "/" are URL prefixes (scheme optional).
func (r *Registry) Blocked(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return true
	}
	host := strings.ToLower(pu.Hostname())
	noScheme := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	for _, b := range r.blocklist {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if !strings.Contains(b, "/") {
			b = strings.ToLower(b)
			if host == b || strings.HasSuffix(host, "."+b) {
				return true
			}
			continue
		}
		bp := strings.TrimPrefix(strings.TrimPrefix(b, "https://"), "http://")
		if strings.HasPrefix(noScheme, bp) {
			return true
		}
	}
	return false
}

// Prunable reports whether a (non-pinned) source should be removed.
func (s *Source) Prunable(now time.Time) bool {
	if s.Pinned {
		return false
	}
	if s.FailStreak >= PruneFails || s.ZeroStreak >= PruneZeroRuns || s.stalePrunable(now) {
		return true
	}
	ref := s.LastOK
	if ref.IsZero() {
		ref = s.Added
	}
	return !ref.IsZero() && now.Sub(ref) > PruneStaleDays*24*time.Hour
}

// Prune removes prunable sources and enforces MaxSources (pinned always kept, then best
// score). Returns the removed URLs.
func (r *Registry) Prune(now time.Time) []string {
	var keep []*Source
	var removed []string
	for _, s := range r.Sources {
		if s.Prunable(now) {
			removed = append(removed, s.URL)
			continue
		}
		keep = append(keep, s)
	}
	r.Sources = keep
	r.sort()
	// sorted pinned-first, so cutting the tail never drops a pinned source
	limit := MaxSources
	for limit < len(r.Sources) && r.Sources[limit].Pinned {
		limit++
	}
	if len(r.Sources) > limit {
		for _, s := range r.Sources[limit:] {
			removed = append(removed, s.URL)
		}
		r.Sources = r.Sources[:limit]
	}
	return removed
}

// RecordFetch updates statistics after a fetch attempt.
func (s *Source) RecordFetch(now time.Time, ok bool, total, valid int) {
	s.LastFetch = now
	if !ok {
		s.FailStreak++
		s.Score *= 0.8
		return
	}
	s.FailStreak = 0
	s.LastOK = now
	s.YieldTotal, s.YieldValid = total, valid
	if valid == 0 {
		s.ZeroStreak++
	} else {
		s.ZeroStreak = 0
	}
}

// RecordAlive folds the number of alive configs from the latest validate run into the score
// (exponential moving average; alive configs dominate, raw valid count is a weak tiebreaker).
func (s *Source) RecordAlive(alive int) {
	s.YieldAlive = alive
	sample := float64(alive) + 0.02*float64(s.YieldValid)
	s.Score = 0.6*s.Score + 0.4*sample
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// readLines returns non-empty, non-# lines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out, sc.Err()
}

// Remove drops a source by (normalized) URL.
func (r *Registry) Remove(u string) {
	n, _ := NormalizeURL(u)
	out := r.Sources[:0]
	for _, s := range r.Sources {
		if s.URL != n {
			out = append(out, s)
		}
	}
	r.Sources = out
}
