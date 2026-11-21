package pipeline

import (
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShardOfDeterministic(t *testing.T) {
	// Golden value: the split must never change silently (FNV-1a 64 of the key, mod N).
	if got := ShardOf("a", 8); got != 4 { // fnv64a("a") = 0xaf63dc4c8601ec8c
		t.Errorf("ShardOf(a,8)=%d want 4", got)
	}
	for i := 0; i < 5; i++ {
		if ShardOf("vless-key-1", 8) != ShardOf("vless-key-1", 8) {
			t.Fatal("ShardOf not stable")
		}
	}
	if ShardOf("anything", 1) != 0 || ShardOf("anything", 0) != 0 {
		t.Error("n<=1 must map to shard 0")
	}
	// Every key lands in exactly one shard and all shards get work.
	var cands []Candidate
	counts := make([]int, 8)
	for i := 0; i < 5000; i++ {
		k := "k" + strconv.Itoa(i)
		s := ShardOf(k, 8)
		if s < 0 || s >= 8 {
			t.Fatalf("shard %d out of range", s)
		}
		counts[s]++
		cands = append(cands, Candidate{Key: k})
	}
	for i, c := range counts {
		if c < 5000/8/2 {
			t.Errorf("shard %d got only %d keys: %v", i, c, counts)
		}
	}
	total := 0
	for i := 0; i < 8; i++ {
		total += len(filterShard(cands, i, 8))
	}
	if total != len(cands) {
		t.Fatalf("shards cover %d of %d", total, len(cands))
	}
	// Independent of candidate order.
	rev := make([]Candidate, len(cands))
	for i, c := range cands {
		rev[len(cands)-1-i] = c
	}
	a, b := filterShard(cands, 3, 8), filterShard(rev, 3, 8)
	if len(a) != len(b) {
		t.Fatal("split depends on order")
	}
}

func TestShardCount(t *testing.T) {
	for _, tc := range []struct{ n, per, max, want int }{
		{0, 3000, 20, 1}, {1, 3000, 20, 1}, {3000, 3000, 20, 1}, {3001, 3000, 20, 2},
		{29500, 3000, 20, 10}, {100000, 3000, 20, 20}, {5, 0, 0, 1},
	} {
		if got := ShardCount(tc.n, tc.per, tc.max); got != tc.want {
			t.Errorf("ShardCount(%d,%d,%d)=%d want %d", tc.n, tc.per, tc.max, got, tc.want)
		}
	}
}

// tcpFixture writes candidates.jsonl + history: even candidates point at a live local
// listener, odd ones at a closed port.
func tcpFixture(t *testing.T, dir string, n int, hist History) []Candidate {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.Addr().(*net.TCPAddr).Port
	dead.Close()
	livePort := ln.Addr().(*net.TCPAddr).Port
	var cands []Candidate
	for i := 0; i < n; i++ {
		port := livePort
		if i%2 == 1 {
			port = deadPort
		}
		cands = append(cands, Candidate{Key: "key-" + strconv.Itoa(i), URI: "vless://u@127.0.0.1:" + strconv.Itoa(port),
			Host: "127.0.0.1", Port: port, Security: "reality"})
	}
	if err := writeJSONL(CandidatesPath(dir), cands); err != nil {
		t.Fatal(err)
	}
	if err := hist.Save(HistoryPath(dir)); err != nil {
		t.Fatal(err)
	}
	return cands
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Sharded validate + merge must give the same alive.jsonl and history as one unsharded run,
// and shard jobs must not touch the shared state.
func TestShardedValidateMergeEqualsUnsharded(t *testing.T) {
	prev := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	now := prev.Add(3 * time.Hour)
	hist := History{}
	for i := 0; i < 40; i++ {
		hist.Observe("key-"+strconv.Itoa(i), true, prev) // streak 1 for all
	}
	hist.Observe("gone-from-sources", true, prev.Add(-30*24*time.Hour)) // expires

	single := t.TempDir()
	cands := tcpFixture(t, single, 40, hist)
	opt := ValidateOptions{TCPTimeout: time.Second, TCPWorkers: 8, SkipXray: true}
	if _, err := Validate(context.Background(), single, opt, now); err != nil {
		t.Fatal(err)
	}

	// Sharded: each shard works on its own copy of the collect output, as on separate runners.
	sharded := t.TempDir()
	copyFile(t, CandidatesPath(single), CandidatesPath(sharded))
	if err := hist.Save(HistoryPath(sharded)); err != nil {
		t.Fatal(err)
	}
	const n = 3
	for i := 0; i < n; i++ {
		runner := t.TempDir()
		copyFile(t, CandidatesPath(sharded), CandidatesPath(runner))
		copyFile(t, HistoryPath(sharded), HistoryPath(runner))
		o := opt
		o.Shard, o.Shards = i, n
		res, err := Validate(context.Background(), runner, o, now)
		if err != nil {
			t.Fatal(err)
		}
		if res.Candidates != len(filterShard(cands, i, n)) {
			t.Errorf("shard %d tested %d", i, res.Candidates)
		}
		if _, err := os.Stat(AlivePath(runner)); err == nil {
			t.Error("shard wrote alive.jsonl")
		}
		if h, _ := LoadHistory(HistoryPath(runner)); !reflect.DeepEqual(h, hist) {
			t.Error("shard modified history")
		}
		// upload-artifact → download-artifact into shards/
		for _, f := range []string{"alive.jsonl", "shard.json"} {
			copyFile(t, filepath.Join(ShardDir(runner, i), f), filepath.Join(ShardDir(sharded, i), f))
		}
	}
	mres, err := Merge(sharded, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(mres.ShardsMissing) != 0 || mres.Alive != 20 || mres.Candidates != 40 || mres.TCPAlive != 20 {
		t.Fatalf("merge result %+v", mres)
	}
	// Same alive set in the same order (tcp_ms is timing noise, so compare keys).
	keys := func(dir string) string {
		a, err := readJSONL(AlivePath(dir))
		if err != nil {
			t.Fatal(err)
		}
		var ks []string
		for _, c := range a {
			ks = append(ks, c.Key)
		}
		return strings.Join(ks, " ")
	}
	if k1, k2 := keys(single), keys(sharded); k1 != k2 {
		t.Errorf("alive.jsonl differs:\n%s\nvs\n%s", k1, k2)
	}
	h1, _ := LoadHistory(HistoryPath(single))
	h2, _ := LoadHistory(HistoryPath(sharded))
	if !reflect.DeepEqual(h1, h2) {
		t.Error("history differs between sharded and unsharded runs")
	}
	if h2["key-0"].OKStreak != 2 || h2["key-1"].OKStreak != 0 {
		t.Errorf("streaks: %+v %+v", h2["key-0"], h2["key-1"])
	}
	if _, ok := h2["gone-from-sources"]; ok {
		t.Error("old entry not expired")
	}
	st, ok := readValidateStats(sharded)
	if !ok || st.Candidates != 40 || st.Alive != 20 || st.Shards != n || !st.SkipXray {
		t.Errorf("validate.json %+v", st)
	}
}

func TestMergeMissingShardAndGuards(t *testing.T) {
	prev := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	now := prev.Add(3 * time.Hour)
	dir := t.TempDir()
	var cands []Candidate
	hist := History{}
	for i := 0; i < 60; i++ {
		k := "key-" + strconv.Itoa(i)
		cands = append(cands, Candidate{Key: k, Host: "h", Port: 1})
		hist.Observe(k, true, prev)
		hist.Observe(k, true, prev) // streak 2
	}
	if err := writeJSONL(CandidatesPath(dir), cands); err != nil {
		t.Fatal(err)
	}
	if err := hist.Save(HistoryPath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dir, "", now); err == nil {
		t.Fatal("merge without shards must fail")
	}
	fp, _ := candidatesFingerprint(dir)
	const n = 4
	// Shards 0, 1, 3 report; everything in them is alive except key-0. Shard 2 is missing.
	for _, i := range []int{3, 0, 1} {
		var alive []Candidate
		for _, c := range filterShard(cands, i, n) {
			if c.Key != "key-0" {
				alive = append(alive, c)
			}
		}
		if err := writeShard(dir, ShardMeta{Shard: i, Shards: n, Fingerprint: fp, Alive: len(alive)}, alive); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Merge(dir, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.ShardsMissing, []int{2}) || res.Untested != len(filterShard(cands, 2, n)) || res.Untested == 0 {
		t.Fatalf("%+v", res)
	}
	h, _ := LoadHistory(HistoryPath(dir))
	for _, c := range cands {
		e := h[c.Key]
		switch {
		case ShardOf(c.Key, n) == 2:
			if e.OKStreak != 2 || !e.LastSeen.Equal(prev) {
				t.Errorf("%s of the missing shard changed: %+v", c.Key, e)
			}
		case c.Key == "key-0":
			if e.OKStreak != 0 {
				t.Errorf("key-0 should be reset: %+v", e)
			}
		default:
			if e.OKStreak != 3 {
				t.Errorf("%s streak %d want 3", c.Key, e.OKStreak)
			}
		}
	}
	// merged alive.jsonl keeps the candidates.jsonl order
	alive, _ := readJSONL(AlivePath(dir))
	last := -1
	for _, c := range alive {
		i, _ := strconv.Atoi(strings.TrimPrefix(c.Key, "key-"))
		if i <= last {
			t.Fatal("alive.jsonl not in candidate order")
		}
		last = i
	}

	// A shard from another collect (different candidates.jsonl) is rejected.
	if err := writeShard(dir, ShardMeta{Shard: 2, Shards: n, Fingerprint: "stale"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dir, "", now); err == nil || !strings.Contains(err.Error(), "different candidates") {
		t.Errorf("stale shard accepted: %v", err)
	}
	// Disagreeing shard counts are rejected.
	if err := writeShard(dir, ShardMeta{Shard: 2, Shards: 5, Fingerprint: fp}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dir, "", now); err == nil {
		t.Error("mixed shard counts accepted")
	}
}

func TestValidateBadShard(t *testing.T) {
	if _, err := Validate(context.Background(), t.TempDir(), ValidateOptions{Shard: 3, Shards: 3}, time.Now()); err == nil {
		t.Error("shard 3 of 3 accepted")
	}
}

func TestWriteAllSplit(t *testing.T) {
	out := t.TempDir()
	var ls []string
	for i := 0; i < 100; i++ {
		ls = append(ls, "vless://line-"+strconv.Itoa(i))
	}
	// leftovers from an earlier, bigger run must disappear
	_ = os.WriteFile(filepath.Join(out, "all-9.txt"), []byte("old"), 0o644)
	names, total, err := writeAll(out, ls, 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 4 || len(names) >= 9 || names[0] != "all-1.txt" {
		t.Fatalf("names %v", names)
	}
	if _, err := os.Stat(filepath.Join(out, "all-9.txt")); err == nil {
		t.Error("stale all-9.txt kept")
	}
	var joined []string
	var sum int64
	for _, n := range names {
		b, _ := os.ReadFile(filepath.Join(out, n))
		eb, _ := os.ReadFile(filepath.Join(out, strings.TrimSuffix(n, ".txt")+".b64"))
		if len(eb) > 600+1 {
			t.Errorf("%s.b64 is %d bytes", n, len(eb))
		}
		dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(eb)))
		if err != nil || string(dec) != string(b) {
			t.Errorf("%s: b64 mismatch", n)
		}
		sum += int64(len(b))
		joined = append(joined, strings.Split(strings.TrimRight(string(b), "\n"), "\n")...)
	}
	if strings.Join(joined, "|") != strings.Join(ls, "|") || sum != total {
		t.Error("split lost or reordered lines")
	}
	// fits → a single all.txt, parts removed
	names, _, err = writeAll(out, ls)
	if err != nil || !reflect.DeepEqual(names, []string{"all.txt"}) {
		t.Fatalf("%v %v", names, err)
	}
	if m, _ := filepath.Glob(filepath.Join(out, "all-*")); len(m) != 0 {
		t.Errorf("stale parts: %v", m)
	}
}
