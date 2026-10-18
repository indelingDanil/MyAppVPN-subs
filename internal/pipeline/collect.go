package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/fetch"
	"github.com/indelingDanil/MyAppVPN-subs/internal/gh"
	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
	"github.com/indelingDanil/MyAppVPN-subs/internal/vless"
)

// CollectOptions tune collect.
type CollectOptions struct {
	RegistryDir    string
	MaxCandidates  int // hard cap on unique candidates (default 20000)
	Workers        int // concurrent source fetches
	PinnedOnly     bool
	FreshnessCalls int           // GitHub commits-API calls allowed per run
	Commits        CommitDater   // nil → GitHub REST with $GITHUB_TOKEN
	Fetcher        *fetch.Client // nil → fetch.New()
}

// CollectResult summarises a collect run.
type CollectResult struct {
	Sources, SourcesOK, Links, Valid, Unique, CommitCalls int
	Excluded                                              []Excluded
}

// Excluded is a source left out of this run, with the reason (shown in meta.json / stats.md).
type Excluded struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
	Pinned bool   `json:"pinned,omitempty"`
}

// ExcludedPath is where collect leaves the excluded list for publish.
func ExcludedPath(dir string) string { return filepath.Join(dir, "excluded.json") }

// Collect fetches every source, extracts and filters vless links, dedupes them and writes
// <dir>/candidates.jsonl, <dir>/excluded.json and the updated <dir>/state/sources.json.
func Collect(ctx context.Context, dir string, opt CollectOptions, now time.Time) (CollectResult, error) {
	var res CollectResult
	if opt.MaxCandidates <= 0 {
		opt.MaxCandidates = 20000
	}
	if opt.Workers <= 0 {
		opt.Workers = 16
	}
	if opt.Commits == nil {
		c := gh.NewGitHub(os.Getenv("GITHUB_TOKEN"))
		c.MaxWait = 30 * time.Second // never stall collect on rate limits; fall back instead
		opt.Commits = c
		if c.Token == "" && opt.FreshnessCalls > 20 {
			opt.FreshnessCalls = 20 // unauthenticated: 60 req/h
		}
	}
	fc := opt.Fetcher
	if fc == nil {
		fc = fetch.New()
	}
	reg, err := registry.Load(opt.RegistryDir, SourcesStatePath(dir), now)
	if err != nil {
		return res, err
	}
	var srcs []*registry.Source
	for _, s := range reg.Ordered() {
		if opt.PinnedOnly && !s.Pinned {
			continue
		}
		if s.Prunable(now) { // waits for the weekly prune instead of being fetched
			continue
		}
		srcs = append(srcs, s) // dead (404) sources ARE re-fetched every run: cheap, and they may come back
	}
	res.Sources = len(srcs)

	type fetched struct {
		resp fetch.Response
		err  error
	}
	got := make([]fetched, len(srcs))
	sem := make(chan struct{}, opt.Workers)
	var wg sync.WaitGroup
	for i, s := range srcs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, u string) {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := fc.Get(ctx, u)
			if r.Truncated {
				log.Printf("collect: %s truncated at %d bytes", u, len(r.Body))
			}
			got[i] = fetched{r, err}
		}(i, s.URL)
	}
	wg.Wait()

	budget := opt.FreshnessCalls
	// Merge in priority order (pinned, then score) so the cap keeps the best sources.
	byKey := map[string]int{}
	var cands []Candidate
	for i, s := range srcs {
		f := got[i]
		if f.err != nil {
			if fetch.Gone(f.resp.Status) {
				s.MarkGone(now, f.resp.Status)
			} else {
				s.RecordFetch(now, false, 0, 0)
			}
			reason := s.ExclusionReason(now)
			if reason == "" {
				reason = "fetch failed: " + f.err.Error()
			}
			res.Excluded = append(res.Excluded, Excluded{s.URL, reason, s.Pinned})
			log.Printf("collect: FAIL %s: %v", s.URL, f.err)
			continue
		}
		sum := sha256.Sum256(f.resp.Body)
		changed := s.ObserveBody(now, hex.EncodeToString(sum[:]))
		before := budget
		UpdateFreshness(ctx, s, f.resp.LastModified, changed, opt.Commits, &budget, now)
		res.CommitCalls += before - budget

		res.SourcesOK++
		st, cfgs := vless.Analyze(f.resp.Body)
		res.Links += st.VlessLinks
		local := map[string]bool{}
		for _, c := range cfgs {
			local[c.Key()] = true
		}
		s.RecordFetch(now, true, st.VlessLinks, len(local))
		if s.Stale {
			res.Excluded = append(res.Excluded, Excluded{s.URL, s.ExclusionReason(now), s.Pinned})
			log.Printf("collect: STALE %s: last update %s (%s)", s.URL, s.LastUpdated.Format("2006-01-02"), s.FreshnessMethod)
			continue
		}
		res.Valid += len(local)
		seen := map[string]bool{}
		for _, c := range cfgs {
			k := c.Key()
			if seen[k] {
				continue
			}
			seen[k] = true
			if idx, ok := byKey[k]; ok {
				if len(cands[idx].Sources) < 3 {
					cands[idx].Sources = append(cands[idx].Sources, s.URL)
				}
				cands[idx].Pinned = cands[idx].Pinned || s.Pinned
				continue
			}
			if len(cands) >= opt.MaxCandidates {
				continue
			}
			byKey[k] = len(cands)
			cands = append(cands, Candidate{
				Key: k, URI: c.Body, Host: c.Host, Port: c.Port,
				Security: c.Security, Transport: c.Transport,
				Sources: []string{s.URL}, Pinned: s.Pinned,
			})
		}
		log.Printf("collect: ok %s: links=%d valid=%d updated=%s (%s)", s.URL, st.VlessLinks, len(local),
			s.LastUpdated.Format("2006-01-02"), s.FreshnessMethod)
	}
	res.Unique = len(cands)
	if err := reg.Save(SourcesStatePath(dir)); err != nil {
		return res, err
	}
	if err := writeExcluded(dir, res.Excluded); err != nil {
		return res, err
	}
	return res, writeJSONL(CandidatesPath(dir), cands)
}

func writeExcluded(dir string, ex []Excluded) error {
	if ex == nil {
		ex = []Excluded{}
	}
	b, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(ExcludedPath(dir), append(b, '\n'), 0o644)
}

func readExcluded(dir string) []Excluded {
	var ex []Excluded
	if b, err := os.ReadFile(ExcludedPath(dir)); err == nil {
		_ = json.Unmarshal(b, &ex)
	}
	if ex == nil {
		ex = []Excluded{}
	}
	return ex
}
