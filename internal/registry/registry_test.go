package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r/blob/main/sub/vless.txt":                    "https://raw.githubusercontent.com/o/r/main/sub/vless.txt",
		"https://github.com/o/r/raw/main/vless.txt":                         "https://raw.githubusercontent.com/o/r/main/vless.txt",
		"https://github.com/o/r/raw/refs/heads/main/vless.txt":              "https://raw.githubusercontent.com/o/r/main/vless.txt",
		"https://github.com/o/r/blob/main/vless.txt?raw=true":               "https://raw.githubusercontent.com/o/r/main/vless.txt",
		"https://raw.githubusercontent.com/o/r/refs/heads/main/a/b.txt":     "https://raw.githubusercontent.com/o/r/main/a/b.txt",
		"http://RAW.githubusercontent.com/o/r/main/x.txt#frag":              "https://raw.githubusercontent.com/o/r/main/x.txt",
		"https://raw.githubusercontent.com/o/r/main/x.txt).":                "https://raw.githubusercontent.com/o/r/main/x.txt",
		"https://cdn.jsdelivr.net/gh/o/r@main/x.txt":                        "https://cdn.jsdelivr.net/gh/o/r@main/x.txt",
		"https://gitlab.com/o/r/-/raw/main/x.txt":                           "https://gitlab.com/o/r/-/raw/main/x.txt",
		"https://raw.githubusercontent.com/o/r/main/%D0%BA%D0%BE%D0%BD.txt": "https://raw.githubusercontent.com/o/r/main/%D0%BA%D0%BE%D0%BD.txt",
	}
	for in, want := range cases {
		got, ok := NormalizeURL(in)
		if !ok || got != want {
			t.Errorf("NormalizeURL(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{
		"https://github.com/o/r", "https://github.com/o/r/issues/1", "ftp://x.com/a.txt",
		"https://raw.githubusercontent.com/o/r", "not a url", "https://user:pw@x.com/a.txt", "https://x.com",
	} {
		if got, ok := NormalizeURL(bad); ok {
			t.Errorf("NormalizeURL(%q) accepted as %q", bad, got)
		}
	}
}

func TestExtractCandidates(t *testing.T) {
	readme := `
Subscriptions:
- [all](https://github.com/o/r/blob/main/all.txt)
- https://raw.githubusercontent.com/o/r/main/vless.txt
- https://raw.githubusercontent.com/o/r/main/vless.txt (duplicate)
- ![badge](https://img.shields.io/badge/x.svg) https://raw.githubusercontent.com/o/r/main/logo.png
- https://example.com/sub.txt  (not a raw host)
- https://cdn.jsdelivr.net/gh/o/r@main/reality.txt
- https://github.com/o/r  (repo page)
`
	want := []string{
		"https://raw.githubusercontent.com/o/r/main/all.txt",
		"https://raw.githubusercontent.com/o/r/main/vless.txt",
		"https://cdn.jsdelivr.net/gh/o/r@main/reality.txt",
	}
	if got := ExtractCandidates(readme); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestRootFileLooksLikeSub(t *testing.T) {
	for name, want := range map[string]bool{
		"vless.txt": true, "Sub_1.txt": true, "configs.txt": true, "README.md": false,
		"main.py": false, "notes.txt": false, "sub": true, "reality_de.txt": true,
	} {
		if got := RootFileLooksLikeSub(name); got != want {
			t.Errorf("%s: %v want %v", name, got, want)
		}
	}
}

func TestPrunable(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	cases := []struct {
		name string
		s    Source
		want bool
	}{
		{"healthy", Source{LastOK: recent, YieldValid: 10}, false},
		{"5 fails", Source{LastOK: recent, FailStreak: 5}, true},
		{"4 fails", Source{LastOK: recent, FailStreak: 4}, false},
		{"3 zero runs", Source{LastOK: recent, ZeroStreak: 3}, true},
		{"2 zero runs", Source{LastOK: recent, ZeroStreak: 2}, false},
		{"stale 31d", Source{LastOK: now.AddDate(0, 0, -31)}, true},
		{"never ok, added 31d ago", Source{Added: now.AddDate(0, 0, -31)}, true},
		{"never ok, added 1d ago", Source{Added: now.AddDate(0, 0, -1)}, false},
		{"pinned dead", Source{Pinned: true, FailStreak: 99, ZeroStreak: 99, LastOK: now.AddDate(-1, 0, 0)}, false},
	}
	for _, tc := range cases {
		if got := tc.s.Prunable(now); got != tc.want {
			t.Errorf("%s: %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestRecordFetchStreaks(t *testing.T) {
	now := time.Now()
	s := &Source{}
	s.RecordFetch(now, true, 10, 0)
	s.RecordFetch(now, true, 10, 0)
	if s.ZeroStreak != 2 || s.FailStreak != 0 {
		t.Fatalf("%+v", s)
	}
	s.RecordFetch(now, false, 0, 0)
	if s.ZeroStreak != 2 || s.FailStreak != 1 {
		t.Fatalf("a failed fetch must not touch the zero streak: %+v", s)
	}
	s.RecordFetch(now, true, 10, 5)
	if s.ZeroStreak != 0 || s.FailStreak != 0 || s.YieldValid != 5 {
		t.Fatalf("%+v", s)
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMergePinnedBlockAndCap(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(dir, "pinned.txt"), "# c\nhttps://github.com/p/p/blob/main/pin.txt\n")
	writeFile(t, filepath.Join(dir, "blocklist.txt"), "bad.example\nraw.githubusercontent.com/evil/\n")
	writeFile(t, filepath.Join(dir, "sources.json"), `[
 {"url":"https://raw.githubusercontent.com/a/a/main/a.txt","added":"2026-10-10T00:00:00Z","score":1,"pinned":true},
 {"url":"https://raw.githubusercontent.com/evil/x/main/a.txt","added":"2026-10-10T00:00:00Z"},
 {"url":"https://cdn.bad.example/gh/x.txt","added":"2026-10-10T00:00:00Z"}
]`)
	state := filepath.Join(dir, "state.json")
	writeFile(t, state, `[{"url":"https://raw.githubusercontent.com/a/a/main/a.txt","last_fetch":"2026-10-17T00:00:00Z","last_ok":"2026-10-17T00:00:00Z","fail_streak":0,"yield_valid":42,"score":7}]`)

	r, err := Load(dir, state, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sources) != 2 {
		t.Fatalf("want a.txt + pinned, got %d: %+v", len(r.Sources), r.Sources)
	}
	var a, pin *Source
	for _, s := range r.Sources {
		switch s.URL {
		case "https://raw.githubusercontent.com/a/a/main/a.txt":
			a = s
		case "https://raw.githubusercontent.com/p/p/main/pin.txt":
			pin = s
		}
	}
	if a == nil || pin == nil {
		t.Fatalf("missing sources: %+v", r.Sources)
	}
	if a.YieldValid != 42 || a.Score != 7 || a.Pinned {
		t.Errorf("state overlay / pinned reset failed: %+v", a)
	}
	if !pin.Pinned {
		t.Error("pinned.txt entry not pinned")
	}
	if r.Add("https://raw.githubusercontent.com/evil/y/main/z.txt", "", now, 1) {
		t.Error("blocked URL added")
	}

	// cap: 305 fresh sources + 1 pinned → 300 kept, pinned survives even with score 0
	for i := 0; i < 305; i++ {
		r.Add("https://raw.githubusercontent.com/o/r/main/"+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt", "o/r", now, float64(i))
	}
	r.Prune(now)
	if len(r.Sources) != MaxSources {
		t.Fatalf("cap: %d", len(r.Sources))
	}
	found := false
	for _, s := range r.Sources {
		if s.Pinned {
			found = true
		}
		if s.Score < 7 && !s.Pinned && s.OriginRepo == "o/r" {
			t.Errorf("low score kept over higher ones: %+v", s)
		}
	}
	if !found {
		t.Error("pinned source pruned by cap")
	}
}
