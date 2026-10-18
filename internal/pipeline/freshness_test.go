package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/fetch"
	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
)

var now0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

type fakeCommits struct {
	date  time.Time
	err   error
	calls int
}

func (f *fakeCommits) LastCommit(_ context.Context, repo, ref, path string) (time.Time, error) {
	f.calls++
	return f.date, f.err
}

const ghURL = "https://raw.githubusercontent.com/o/r/main/sub.txt"

func TestUpdateFreshnessGitHubCaching(t *testing.T) {
	fc := &fakeCommits{date: now0.Add(-48 * time.Hour)}
	s := &registry.Source{URL: ghURL}
	budget := 10
	changed := s.ObserveBody(now0, "a")
	UpdateFreshness(context.Background(), s, time.Time{}, changed, fc, &budget, now0)
	if fc.calls != 1 || s.FreshnessMethod != registry.MethodGitHub || !s.LastUpdated.Equal(fc.date) || s.Stale {
		t.Fatalf("first run: calls=%d %+v", fc.calls, s)
	}
	// unchanged body next run → cached, no API call
	later := now0.Add(3 * time.Hour)
	changed = s.ObserveBody(later, "a")
	UpdateFreshness(context.Background(), s, time.Time{}, changed, fc, &budget, later)
	if fc.calls != 1 {
		t.Fatalf("unchanged body must not call the API (calls=%d)", fc.calls)
	}
	// 8 days later, still unchanged → stale from the cached commit date
	later = now0.Add(8 * 24 * time.Hour)
	changed = s.ObserveBody(later, "a")
	UpdateFreshness(context.Background(), s, time.Time{}, changed, fc, &budget, later)
	if !s.Stale || fc.calls != 1 {
		t.Fatalf("expected stale via cached date: %+v calls=%d", s, fc.calls)
	}
	// body changes and a new commit appears → fresh again
	fc.date = later.Add(-time.Hour)
	changed = s.ObserveBody(later, "b")
	UpdateFreshness(context.Background(), s, time.Time{}, changed, fc, &budget, later)
	if s.Stale || fc.calls != 2 {
		t.Fatalf("expected revival: %+v calls=%d", s, fc.calls)
	}
}

func TestUpdateFreshnessFallbacks(t *testing.T) {
	// API error → Last-Modified
	fc := &fakeCommits{err: errors.New("rate limited")}
	s := &registry.Source{URL: ghURL}
	budget := 5
	lm := now0.Add(-10 * 24 * time.Hour)
	UpdateFreshness(context.Background(), s, lm, s.ObserveBody(now0, "a"), fc, &budget, now0)
	if s.FreshnessMethod != registry.MethodLastModified || !s.Stale {
		t.Fatalf("expected last-modified stale: %+v", s)
	}
	// budget exhausted, nothing cached, no header → hash (not judged on first sight)
	s2 := &registry.Source{URL: ghURL}
	zero := 0
	UpdateFreshness(context.Background(), s2, time.Time{}, s2.ObserveBody(now0, "a"), fc, &zero, now0)
	if s2.FreshnessMethod != registry.MethodHash || s2.Stale {
		t.Fatalf("expected hash, not stale: %+v", s2)
	}
	// non-GitHub host never calls the API
	fc3 := &fakeCommits{date: now0}
	s3 := &registry.Source{URL: "https://example.com/sub.txt"}
	UpdateFreshness(context.Background(), s3, time.Time{}, s3.ObserveBody(now0, "a"), fc3, &budget, now0)
	if fc3.calls != 0 || s3.FreshnessMethod != registry.MethodHash {
		t.Fatalf("non-GitHub: calls=%d %+v", fc3.calls, s3)
	}
}

// End-to-end collect against a local TLS server: a fresh source, a 404 pinned source and a
// stale (old Last-Modified) source. Only the fresh one contributes configs; the other two are
// reported with their reasons in excluded.json and stay registered.
func TestCollectExcludesGoneAndStale(t *testing.T) {
	line := func(n string) string {
		return "vless://4cb9157c-cd6f-451f-b786-1c92edec32cb@1.2.3." + n + ":443?security=reality&pbk=k&sni=s.com\n"
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fresh.txt":
			w.Header().Set("Last-Modified", now0.Add(-time.Hour).Format(http.TimeFormat))
			_, _ = w.Write([]byte(line("1") + line("2")))
		case "/stale.txt":
			w.Header().Set("Last-Modified", now0.Add(-9*24*time.Hour).Format(http.TimeFormat))
			_, _ = w.Write([]byte(line("3")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := t.TempDir()
	if err := os.WriteFile(filepath.Join(reg, "pinned.txt"), []byte(srv.URL+"/gone.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `[{"url":"` + srv.URL + `/fresh.txt","added":"2026-10-01T00:00:00Z"},{"url":"` + srv.URL + `/stale.txt","added":"2026-10-01T00:00:00Z"}]`
	if err := os.WriteFile(filepath.Join(reg, "sources.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fc := fetch.New()
	fc.HTTP = srv.Client()
	fc.Retries = 0
	res, err := Collect(context.Background(), dir, CollectOptions{RegistryDir: reg, Fetcher: fc, Commits: &fakeCommits{}, FreshnessCalls: 5}, now0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unique != 2 {
		t.Errorf("unique=%d want 2 (only fresh.txt)", res.Unique)
	}
	reasons := map[string]string{}
	for _, e := range readExcluded(dir) {
		reasons[e.URL[strings.LastIndex(e.URL, "/")+1:]] = e.Reason
	}
	if !strings.HasPrefix(reasons["gone.txt"], "404") || reasons["stale.txt"] != "stale 9d" || reasons["fresh.txt"] != "" {
		t.Errorf("reasons: %v", reasons)
	}
	r, err := registry.Load(reg, SourcesStatePath(dir), now0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sources) != 3 {
		t.Fatalf("excluded sources must stay registered: %d", len(r.Sources))
	}
	for _, s := range r.Sources {
		if strings.HasSuffix(s.URL, "gone.txt") && (!s.Pinned || !s.Dead() || s.FailStreak != 0) {
			t.Errorf("gone pinned: %+v", s)
		}
		if strings.HasSuffix(s.URL, "stale.txt") && (!s.Stale || s.FreshnessMethod != registry.MethodLastModified) {
			t.Errorf("stale: %+v", s)
		}
	}
}
