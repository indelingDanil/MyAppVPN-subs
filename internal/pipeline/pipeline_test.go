package pipeline

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRankOrder(t *testing.T) {
	items := []ranked{
		{Candidate: Candidate{Key: "tls-fast", Security: "tls", DelayMs: 50}, Streak: 9},
		{Candidate: Candidate{Key: "r-new-fast", Security: "reality", DelayMs: 100}, Streak: 0},
		{Candidate: Candidate{Key: "r-stable-slow", Security: "reality", DelayMs: 900}, Streak: 5},
		{Candidate: Candidate{Key: "r-stable-fast", Security: "reality", DelayMs: 300}, Streak: 5},
		{Candidate: Candidate{Key: "r-stable-tcponly", Security: "reality", TCPms: 5}, Streak: 5},
	}
	Rank(items)
	var got []string
	for _, it := range items {
		got = append(got, it.Key)
	}
	want := "r-stable-fast r-stable-slow r-stable-tcponly r-new-fast tls-fast"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestHistory(t *testing.T) {
	h := History{}
	now := time.Now()
	h.Observe("k", true, now)
	h.Observe("k", true, now)
	if h["k"].OKStreak != 2 {
		t.Fatal(h["k"])
	}
	h.Observe("k", false, now)
	if h["k"].OKStreak != 0 || h["k"].LastOK.IsZero() {
		t.Fatal(h["k"])
	}
	h.Expire(now.Add((HistoryKeepDays + 1) * 24 * time.Hour))
	if len(h) != 0 {
		t.Fatal("not expired")
	}
}

func TestUntag(t *testing.T) {
	for in, want := range map[string]int{
		"vless://u@h:1?security=tls#mapps-42":    42,
		"vless://u@h:1?security=tls#mapps-7%20x": 7,
		"vless://u@h:1#%6Dapps-3":                3,
	} {
		if n, ok := untag(in); !ok || n != want {
			t.Errorf("untag(%q)=%d,%v", in, n, ok)
		}
	}
	if _, ok := untag("vless://u@h:1#other"); ok {
		t.Error("untagged link matched")
	}
}

func TestDecodeXK(t *testing.T) {
	r := decodeXK(map[string]any{"link": "vless://x#mapps-1", "status": "passed", "delay": 570.0, "ip": "null", "location": "DE"})
	if !r.Passed || r.Delay != 570 || r.IP != "" || r.CC != "DE" {
		t.Fatalf("%+v", r)
	}
	if decodeXK(map[string]any{"link": "x", "status": "failed", "delay": 0.0}).Passed {
		t.Fatal("failed counted as passed")
	}
	if decodeXK(map[string]any{"link": "x", "status": "semi-passed", "delay": 10.0}).Passed {
		t.Fatal("semi-passed counted as passed")
	}
}

// End-to-end publish on a synthetic alive.jsonl (no mmdb: countries come from ExitCC).
func TestPublishContract(t *testing.T) {
	dir := t.TempDir()
	var alive []Candidate
	ccs := []string{"DE", "DE", "NL", "RU", "UA", "", "US"}
	for i := 0; i < 140; i++ {
		sec := "tls"
		q := "security=tls&type=ws&sni=s.com&path=%2F" + strconv.Itoa(i)
		if i%2 == 0 {
			sec = "reality"
			q = "security=reality&pbk=key" + strconv.Itoa(i) + "&sni=s.com&flow=xtls-rprx-vision"
		}
		alive = append(alive, Candidate{
			Key: "k" + strconv.Itoa(i), URI: "vless://4cb9157c-cd6f-451f-b786-1c92edec32cb@h" + strconv.Itoa(i) + ".com:443?" + q,
			Host: "h" + strconv.Itoa(i) + ".com", Port: 443, Security: sec, Transport: "tcp",
			DelayMs: 100 + i, ExitCC: ccs[i%len(ccs)],
		})
	}
	// one that the app would drop must never be published
	alive = append(alive, Candidate{Key: "bad", URI: "vless://u@x.com:443?security=none", Security: "none", DelayMs: 1, ExitCC: "DE"})
	if err := writeJSONL(AlivePath(dir), alive); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	res, err := Publish(dir, PublishOptions{OutDir: out, MaxPerFile: 500, PerCountry: 15, PerCountryReality: 60, MinTotal: 1, SourcesUsed: 3}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(out, 500); err != nil {
		t.Fatal(err)
	}
	// per-country cap: DE, NL, US, XX → ≤15 each
	if res.Total != 15*4 {
		t.Errorf("total=%d want 60", res.Total)
	}
	if _, err := os.Stat(filepath.Join(out, "by-country", "RU.txt")); err != nil {
		t.Error("RU.txt missing")
	}
	if _, err := os.Stat(filepath.Join(out, "by-country", "UA.txt")); err == nil {
		t.Error("UA.txt must not exist")
	}
	b, _ := os.ReadFile(filepath.Join(out, "vless.txt"))
	first := strings.SplitN(string(b), "\n", 2)[0]
	if !strings.Contains(first, "security=reality") || !strings.Contains(first, "#%F0%9F%87") {
		t.Errorf("first line should be reality with a flag remark: %s", first)
	}
	for _, f := range []string{"vless.b64", "reality.txt", "reality.b64", "meta.json", "stats.md"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("%s missing", f)
		}
	}
	meta, _ := os.ReadFile(filepath.Join(out, "meta.json"))
	for _, k := range []string{`"schema": 1`, `"updated_at"`, `"by_country"`, `"sources_used": 3`} {
		if !strings.Contains(string(meta), k) {
			t.Errorf("meta.json lacks %s", k)
		}
	}
	// min-total guard
	if _, err := Publish(dir, PublishOptions{OutDir: out, MaxPerFile: 500, PerCountry: 15, MinTotal: 1000}, time.Now()); err == nil {
		t.Error("min-total guard did not trigger")
	}
}
