package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
)

// Sharding. validate --shard i --shards N tests only the candidates with ShardOf(key, N) == i
// and writes its result to <dir>/shards/<i>/ (alive.jsonl + shard.json) without touching the
// shared state. merge then combines every shard that reported back: it writes <dir>/alive.jsonl,
// updates state/history.json and the per-source alive stats exactly once, the same way an
// unsharded validate does. Candidates of a shard that did not report are left out of the
// history update (no false "dead" streak resets) and of the per-source stats.

// MaxShards bounds --shards (GitHub's matrix limit is 256; the workflow uses far fewer).
const MaxShards = 256

// ShardOf returns the shard (0..n-1) a candidate key belongs to. It depends only on the key,
// so the split is stable across runs, machines and candidate order.
func ShardOf(key string, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(n))
}

// ShardsDir holds the per-shard results.
func ShardsDir(dir string) string            { return filepath.Join(dir, "shards") }
func ShardDir(dir string, i int) string      { return filepath.Join(ShardsDir(dir), strconv.Itoa(i)) }
func shardMetaPath(dir string, i int) string { return filepath.Join(ShardDir(dir, i), "shard.json") }

// ValidateStatsPath is where validate (unsharded) or merge leaves the run counts for publish.
func ValidateStatsPath(dir string) string { return filepath.Join(dir, "validate.json") }

// ShardMeta is written next to a shard's alive.jsonl.
type ShardMeta struct {
	Shard       int    `json:"shard"`
	Shards      int    `json:"shards"`
	Fingerprint string `json:"candidates_sha256"` // of candidates.jsonl, so stale shards are rejected
	Candidates  int    `json:"candidates"`
	TCPAlive    int    `json:"tcp_alive"`
	XrayTested  int    `json:"xray_tested"`
	Alive       int    `json:"alive"`
	SkipXray    bool   `json:"skip_xray"`
}

// ValidateStats are the run counts shown in meta.json / stats.md.
type ValidateStats struct {
	Candidates    int   `json:"candidates"` // unique configs collected
	TCPAlive      int   `json:"tcp_alive"`
	XrayTested    int   `json:"xray_tested"`
	Alive         int   `json:"alive"` // passed xray (or TCP when xray was skipped)
	SkipXray      bool  `json:"skip_xray"`
	Shards        int   `json:"shards"`
	ShardsMissing []int `json:"shards_missing,omitempty"`
	Untested      int   `json:"untested,omitempty"` // candidates in missing shards
}

func candidatesFingerprint(dir string) (string, error) {
	b, err := os.ReadFile(CandidatesPath(dir))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// filterShard keeps the candidates of shard i out of n.
func filterShard(cands []Candidate, i, n int) []Candidate {
	if n <= 1 {
		return cands
	}
	var out []Candidate
	for _, c := range cands {
		if ShardOf(c.Key, n) == i {
			out = append(out, c)
		}
	}
	return out
}

func writeShard(dir string, m ShardMeta, alive []Candidate) error {
	if err := writeJSONL(filepath.Join(ShardDir(dir, m.Shard), "alive.jsonl"), alive); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(shardMetaPath(dir, m.Shard), append(b, '\n'), 0o644)
}

// MergeResult summarises a merge.
type MergeResult struct {
	ValidateStats
	Present []int
}

// Merge combines <dir>/shards/*/ into <dir>/alive.jsonl and updates history / source stats.
// It fails when no shard reported, when shards disagree on N or come from another collect.
func Merge(dir, registryDir string, now time.Time) (MergeResult, error) {
	var res MergeResult
	cands, err := readJSONL(CandidatesPath(dir))
	if err != nil {
		return res, fmt.Errorf("read candidates: %w", err)
	}
	fp, err := candidatesFingerprint(dir)
	if err != nil {
		return res, err
	}
	metas, _ := filepath.Glob(filepath.Join(ShardsDir(dir), "*", "shard.json"))
	if len(metas) == 0 {
		return res, fmt.Errorf("no shard results in %s", ShardsDir(dir))
	}
	n := 0
	present := map[int]bool{}
	var alive []Candidate
	skipXray := false
	for _, p := range metas {
		var m ShardMeta
		b, err := os.ReadFile(p)
		if err != nil {
			return res, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return res, fmt.Errorf("%s: %w", p, err)
		}
		if n == 0 {
			n = m.Shards
		}
		if m.Shards != n || m.Shard < 0 || m.Shard >= n {
			return res, fmt.Errorf("%s: shard %d/%d does not match %d shards", p, m.Shard, m.Shards, n)
		}
		if m.Fingerprint != fp {
			return res, fmt.Errorf("%s: shard %d was validated against a different candidates.jsonl", p, m.Shard)
		}
		if present[m.Shard] {
			return res, fmt.Errorf("shard %d reported twice", m.Shard)
		}
		a, err := readJSONL(filepath.Join(filepath.Dir(p), "alive.jsonl"))
		if err != nil {
			return res, fmt.Errorf("shard %d alive.jsonl: %w", m.Shard, err)
		}
		for _, c := range a {
			if ShardOf(c.Key, n) != m.Shard {
				return res, fmt.Errorf("shard %d returned key %s of shard %d", m.Shard, c.Key, ShardOf(c.Key, n))
			}
		}
		present[m.Shard] = true
		alive = append(alive, a...)
		res.TCPAlive += m.TCPAlive
		res.XrayTested += m.XrayTested
		skipXray = skipXray || m.SkipXray
	}
	for i := 0; i < n; i++ {
		if present[i] {
			res.Present = append(res.Present, i)
		} else {
			res.ShardsMissing = append(res.ShardsMissing, i)
		}
	}
	// Candidates of missing shards were not tested: keep them out of the history update.
	var tested []Candidate
	for _, c := range cands {
		if present[ShardOf(c.Key, n)] {
			tested = append(tested, c)
		} else {
			res.Untested++
		}
	}
	// Deterministic order: as in candidates.jsonl.
	idx := make(map[string]int, len(cands))
	for i, c := range cands {
		idx[c.Key] = i
	}
	sort.SliceStable(alive, func(i, j int) bool { return idx[alive[i].Key] < idx[alive[j].Key] })

	res.Candidates, res.Alive, res.SkipXray, res.Shards = len(cands), len(alive), skipXray, n
	if len(res.ShardsMissing) > 0 {
		log.Printf("merge: WARNING shards %v missing — %d candidates untested this run (history left unchanged for them)", res.ShardsMissing, res.Untested)
	}
	err = applyResults(dir, registryDir, tested, alive, len(res.ShardsMissing) == 0, res.ValidateStats, now)
	return res, err
}

// applyResults is the single place that updates shared state after validation: history for
// every tested candidate, per-source alive stats (only when every candidate was tested, else a
// partial count would wrongly lower source scores), alive.jsonl and validate.json.
func applyResults(dir, registryDir string, tested, alive []Candidate, complete bool, st ValidateStats, now time.Time) error {
	hist, err := LoadHistory(HistoryPath(dir))
	if err != nil {
		return err
	}
	ok := make(map[string]bool, len(alive))
	for _, c := range alive {
		ok[c.Key] = true
	}
	for _, c := range tested {
		hist.Observe(c.Key, ok[c.Key], now)
	}
	hist.Expire(now)
	if err := hist.Save(HistoryPath(dir)); err != nil {
		return err
	}

	if registryDir != "" && complete {
		perSrc := map[string]int{}
		for _, c := range alive {
			for _, s := range c.Sources {
				perSrc[s]++
			}
		}
		reg, err := registry.Load(registryDir, SourcesStatePath(dir), now)
		if err != nil {
			return err
		}
		// "Fetched this run" = stamped by the latest collect. Collect stamps every source it
		// tried with the same time, so this holds however long the (sharded) validation took.
		var collectedAt time.Time
		for _, s := range reg.Sources {
			if s.LastFetch.After(collectedAt) {
				collectedAt = s.LastFetch
			}
		}
		for _, s := range reg.Sources {
			if !collectedAt.IsZero() && s.LastFetch.Equal(collectedAt) && now.Sub(collectedAt) < 6*time.Hour {
				s.RecordAlive(perSrc[s.URL])
			}
		}
		if err := reg.Save(SourcesStatePath(dir)); err != nil {
			return err
		}
	} else if registryDir != "" {
		log.Printf("validate: per-source alive stats not updated (incomplete run)")
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(ValidateStatsPath(dir), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return writeJSONL(AlivePath(dir), alive)
}

func readValidateStats(dir string) (ValidateStats, bool) {
	var st ValidateStats
	b, err := os.ReadFile(ValidateStatsPath(dir))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return st, false
	}
	return st, true
}

// ShardCount picks how many validate shards a run needs: ceil(candidates/perShard),
// at least 1, at most maxShards (the per-shard load then grows instead of being capped).
func ShardCount(candidates, perShard, maxShards int) int {
	if perShard <= 0 {
		perShard = 3000
	}
	if maxShards <= 0 || maxShards > MaxShards {
		maxShards = MaxShards
	}
	n := (candidates + perShard - 1) / perShard
	return min(max(n, 1), maxShards)
}

// CountCandidates returns the number of lines in candidates.jsonl.
func CountCandidates(dir string) (int, error) {
	c, err := readJSONL(CandidatesPath(dir))
	return len(c), err
}
