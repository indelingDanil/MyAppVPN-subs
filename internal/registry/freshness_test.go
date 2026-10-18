package registry

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func TestEvaluateStaleAuthoritative(t *testing.T) {
	for _, m := range []string{MethodGitHub, MethodLastModified} {
		s := &Source{}
		s.SetUpdated(t0.Add(-days(6)), m)
		if s.EvaluateStale(t0) {
			t.Errorf("%s: 6d old must be fresh", m)
		}
		s.SetUpdated(t0.Add(-days(7)-time.Minute), m)
		if !s.EvaluateStale(t0) {
			t.Errorf("%s: >7d old must be stale", m)
		}
		if r := s.ExclusionReason(t0); r != "stale 7d" {
			t.Errorf("%s: reason %q", m, r)
		}
	}
	if (&Source{}).EvaluateStale(t0) {
		t.Error("unknown update time must not be stale")
	}
}

func TestHashFreshnessNeedsObservationWindow(t *testing.T) {
	s := &Source{}
	start := t0.Add(-days(10))
	if !s.ObserveBody(start, "h1") {
		t.Fatal("first observation must count as change")
	}
	// same body every run for 6 days: observed < 7d → not judged yet
	now := start.Add(days(6))
	if s.ObserveBody(now, "h1") {
		t.Fatal("same hash reported as changed")
	}
	s.UseHashFreshness()
	if s.EvaluateStale(now) {
		t.Fatal("stale before 7 days of observation")
	}
	// day 8, still unchanged → stale
	now = start.Add(days(8))
	s.ObserveBody(now, "h1")
	s.UseHashFreshness()
	if !s.EvaluateStale(now) || s.FreshnessMethod != MethodHash {
		t.Fatalf("expected stale by hash: %+v", s)
	}
	// content changes → fresh again (comes back automatically)
	now = start.Add(days(9))
	if !s.ObserveBody(now, "h2") {
		t.Fatal("hash change not detected")
	}
	s.UseHashFreshness()
	if s.EvaluateStale(now) || !s.LastUpdated.Equal(now) {
		t.Fatalf("updated source must come back: %+v", s)
	}
}

func TestGoneAndRevive(t *testing.T) {
	s := &Source{Pinned: true}
	s.MarkGone(t0.Add(-days(2)), 404)
	s.MarkGone(t0, 404)
	if !s.Dead() || !s.DeadSince.Equal(t0.Add(-days(2))) {
		t.Fatalf("dead_since must keep the first outage time: %+v", s)
	}
	if r := s.ExclusionReason(t0); !strings.HasPrefix(r, "404") {
		t.Errorf("reason %q", r)
	}
	if s.Prunable(t0.Add(days(365))) {
		t.Error("pinned dead source must never be prunable")
	}
	s.ObserveBody(t0, "x")
	if s.Dead() || s.ExclusionReason(t0) != "" {
		t.Errorf("200 must revive: %+v", s)
	}
}

func TestStalePrune(t *testing.T) {
	mk := func(pinned bool, age int) *Source {
		s := &Source{Pinned: pinned, LastOK: t0}
		s.SetUpdated(t0.Add(-days(age)), MethodGitHub)
		s.EvaluateStale(t0)
		return s
	}
	if !mk(false, 31).Prunable(t0) {
		t.Error("non-pinned stale 31d must be pruned")
	}
	if mk(false, 20).Prunable(t0) {
		t.Error("stale 20d is excluded but kept")
	}
	if mk(true, 300).Prunable(t0) {
		t.Error("pinned stale must be kept")
	}
}

func TestGitHubFileRef(t *testing.T) {
	cases := map[string]GitHubFile{
		"https://raw.githubusercontent.com/o/r/main/dir/f.txt":       {"o/r", "main", "dir/f.txt"},
		"https://raw.githubusercontent.com/o/r/refs/heads/dev/f.txt": {"o/r", "dev", "f.txt"},
		"https://github.com/o/r/blob/main/f.txt":                     {"o/r", "main", "f.txt"},
		"https://cdn.jsdelivr.net/gh/o/r@main/a/b.txt":               {"o/r", "main", "a/b.txt"},
		"https://cdn.jsdelivr.net/gh/o/r/a.txt":                      {"o/r", "", "a.txt"},
	}
	for in, want := range cases {
		got, ok := GitHubFileRef(in)
		if !ok || got != want {
			t.Errorf("%s: %+v %v want %+v", in, got, ok, want)
		}
	}
	if _, ok := GitHubFileRef("https://gitlab.com/o/r/-/raw/main/f.txt"); ok {
		t.Error("gitlab is not a GitHub file")
	}
}
