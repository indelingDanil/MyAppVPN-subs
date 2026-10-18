package discover

import (
	"context"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/fetch"
	"github.com/indelingDanil/MyAppVPN-subs/internal/gh"
	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
	"github.com/indelingDanil/MyAppVPN-subs/internal/vless"
)

// DefaultQueries are the GitHub repository searches (a pushed:>= filter is appended).
var DefaultQueries = []string{
	"topic:v2ray-config",
	"topic:v2ray-configs",
	"topic:vless",
	"topic:xray-config",
	"vless subscription in:readme,description",
	"vless reality configs in:readme,description",
}

// Options tune a discovery run.
type Options struct {
	Queries         []string
	Days            int // only repos pushed within this many days
	Pages           int // search pages per query
	PerPage         int
	MaxRepos        int // repos inspected per run
	MaxPerRepo      int // candidate URLs probed per repo
	MaxProbes       int // total candidate bodies fetched
	MaxRootProbes   int // root-tree API calls
	ProbeWorkers    int
	ProbeTimeoutSec int
}

// DefaultOptions are sized for the Actions GITHUB_TOKEN (1000 REST req/h per repo, 30 search/min).
func DefaultOptions() Options {
	return Options{Queries: DefaultQueries, Days: 7, Pages: 3, PerPage: 50, MaxRepos: 150,
		MaxPerRepo: 8, MaxProbes: 600, MaxRootProbes: 150, ProbeWorkers: 8, ProbeTimeoutSec: 20}
}

// Result summarises a run.
type Result struct {
	Repos, Candidates, Probed, Accepted int
	Added                               []string
}

// Run searches GitHub, extracts candidate URLs and adds the accepted ones to reg.
func Run(ctx context.Context, client *gh.GitHub, reg *registry.Registry, opt Options, now time.Time) Result {
	var res Result
	since := now.AddDate(0, 0, -opt.Days).Format("2006-01-02")
	seenRepo := map[string]bool{}
	var repos []gh.Repo
	for _, q := range opt.Queries {
		items, err := client.SearchRepos(ctx, q+" pushed:>="+since, opt.Pages, opt.PerPage)
		if err != nil {
			log.Printf("discover: search %q: %v", q, err)
		}
		for _, r := range items {
			if r.Archived || seenRepo[r.FullName] {
				continue
			}
			seenRepo[r.FullName] = true
			repos = append(repos, r)
		}
		time.Sleep(2500 * time.Millisecond)
	}
	if len(repos) > opt.MaxRepos {
		repos = repos[:opt.MaxRepos]
	}
	res.Repos = len(repos)

	type cand struct{ url, repo string }
	var cands []cand
	seen := map[string]bool{}
	rootCalls := 0
	for _, r := range repos {
		var urls []string
		if txt, err := client.Readme(ctx, r.FullName); err == nil {
			urls = append(urls, filterNoise(registry.ExtractCandidates(txt))...)
		}
		if rootCalls < opt.MaxRootProbes && r.DefaultBranch != "" {
			rootCalls++
			if files, err := client.RootFiles(ctx, r.FullName, r.DefaultBranch); err == nil {
				for _, f := range files {
					if registry.RootFileLooksLikeSub(f) {
						urls = append(urls, "https://raw.githubusercontent.com/"+r.FullName+"/"+r.DefaultBranch+"/"+url.PathEscape(f))
					}
				}
			}
		}
		n := 0
		for _, u := range urls {
			nu, ok := registry.NormalizeURL(u)
			if !ok || seen[nu] || reg.Has(nu) || reg.Blocked(nu) {
				continue
			}
			seen[nu] = true
			cands = append(cands, cand{nu, r.FullName})
			if n++; n >= opt.MaxPerRepo {
				break
			}
		}
	}
	if len(cands) > opt.MaxProbes {
		cands = cands[:opt.MaxProbes]
	}
	res.Candidates = len(cands)

	fc := fetch.New()
	fc.Timeout = time.Duration(opt.ProbeTimeoutSec) * time.Second
	fc.Retries = 1
	var mu sync.Mutex
	sem := make(chan struct{}, opt.ProbeWorkers)
	var wg sync.WaitGroup
	for _, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(c cand) {
			defer wg.Done()
			defer func() { <-sem }()
			resp, err := fc.Get(ctx, c.url)
			mu.Lock()
			defer mu.Unlock()
			res.Probed++
			if err != nil {
				return
			}
			st, _ := vless.Analyze(resp.Body)
			if !st.LooksLikeSubscription() {
				return
			}
			res.Accepted++
			// initial score: a fraction of the valid count, so new sources start below
			// proven ones but above dead ones.
			score := float64(st.Valid) * 0.05
			if score > 10 {
				score = 10
			}
			if reg.Add(c.url, c.repo, now, score) {
				res.Added = append(res.Added, c.url)
			}
		}(c)
	}
	wg.Wait()
	return res
}

// filterNoise drops obvious non-subscription paths.
func filterNoise(urls []string) []string {
	var out []string
	for _, u := range urls {
		l := strings.ToLower(u)
		if strings.Contains(l, "/license") || strings.Contains(l, "requirements.txt") || strings.Contains(l, "/.github/") {
			continue
		}
		out = append(out, u)
	}
	return out
}

// Getter is the subset of fetch.Client used by RecheckDead (fakeable in tests).
type Getter interface {
	Get(ctx context.Context, url string) (fetch.Response, error)
}

// RecheckDead re-fetches every NON-pinned source currently marked gone (404/410/451).
// Still gone → removed from the registry; answers 200 → revived (dead_since cleared).
// Other errors (timeouts, 5xx) leave it as is. Pinned sources are never removed here;
// collect re-checks them every run.
func RecheckDead(ctx context.Context, reg *registry.Registry, g Getter, now time.Time) (removed, revived []string) {
	for _, s := range append([]*registry.Source(nil), reg.Sources...) {
		if s.Pinned || !s.Dead() {
			continue
		}
		resp, err := g.Get(ctx, s.URL)
		switch {
		case err == nil:
			s.DeadSince, s.DeadStatus = time.Time{}, 0
			revived = append(revived, s.URL)
		case fetch.Gone(resp.Status):
			reg.Remove(s.URL)
			removed = append(removed, s.URL)
		}
	}
	return removed, revived
}
