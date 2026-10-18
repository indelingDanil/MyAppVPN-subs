package discover

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/fetch"
	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
)

func TestFilterNoise(t *testing.T) {
	got := filterNoise([]string{"https://raw.githubusercontent.com/o/r/main/LICENSE", "https://raw.githubusercontent.com/o/r/main/sub.txt"})
	if len(got) != 1 {
		t.Fatal(got)
	}
}

type fakeGetter map[string]int

func (f fakeGetter) Get(_ context.Context, u string) (fetch.Response, error) {
	st := f[u]
	if st == 200 {
		return fetch.Response{Status: 200}, nil
	}
	return fetch.Response{Status: st}, errors.New("http error")
}

func TestRecheckDead(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	reg := &registry.Registry{}
	mk := func(u string, pinned bool) {
		s := &registry.Source{URL: u, Pinned: pinned, LastOK: now}
		s.MarkGone(now.Add(-time.Hour), 404)
		reg.Sources = append(reg.Sources, s)
	}
	a, b, c, d := "https://h.com/a.txt", "https://h.com/b.txt", "https://h.com/c.txt", "https://h.com/d.txt"
	mk(a, false) // still 404 → removed
	mk(b, false) // back to 200 → revived
	mk(c, true)  // pinned → untouched
	mk(d, false) // timeout (status 0) → kept, still dead
	removed, revived := RecheckDead(context.Background(), reg, fakeGetter{a: 404, b: 200, c: 404, d: 0}, now)
	if len(removed) != 1 || removed[0] != a || len(revived) != 1 || revived[0] != b {
		t.Fatalf("removed=%v revived=%v", removed, revived)
	}
	if len(reg.Sources) != 3 {
		t.Fatalf("sources: %d", len(reg.Sources))
	}
	for _, s := range reg.Sources {
		if (s.URL == b) == s.Dead() {
			t.Errorf("%s dead=%v", s.URL, s.Dead())
		}
	}
}
