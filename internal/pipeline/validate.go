package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
	"github.com/indelingDanil/MyAppVPN-subs/internal/vless"
)

// ValidateOptions tune validate.
type ValidateOptions struct {
	RegistryDir string
	TCPTimeout  time.Duration
	TCPWorkers  int
	SkipXray    bool
	XrayKnife   string
	XKThreads   int
	XKTimeoutMs int
	XKURL       string
	MaxXray     int
	XKRealIP    bool // ask xray-knife for the egress IP + country (one extra request per config)
}

// ValidateResult summarises a validate run.
type ValidateResult struct {
	Candidates, TCPAlive, XrayTested, Alive int
}

// Validate runs the liveness checks (no speed filtering: runners are in US/EU, users in RU)
// and writes <dir>/alive.jsonl, updating history and per-source alive stats.
func Validate(ctx context.Context, dir string, opt ValidateOptions, now time.Time) (ValidateResult, error) {
	var res ValidateResult
	cands, err := readJSONL(CandidatesPath(dir))
	if err != nil {
		return res, fmt.Errorf("read candidates (run collect first): %w", err)
	}
	res.Candidates = len(cands)
	hist, err := LoadHistory(HistoryPath(dir))
	if err != nil {
		return res, err
	}

	// Stage 1: TCP connect.
	alive := tcpStage(ctx, cands, opt.TCPTimeout, opt.TCPWorkers)
	res.TCPAlive = len(alive)
	log.Printf("validate: TCP stage %d/%d alive", len(alive), len(cands))

	// Stage 2: real delay through xray-knife.
	if !opt.SkipXray {
		// Prefer historically stable, pinned, fast-to-connect configs if we must cap.
		sort.SliceStable(alive, func(i, j int) bool {
			hi, hj := streak(hist, alive[i].Key), streak(hist, alive[j].Key)
			if hi != hj {
				return hi > hj
			}
			if alive[i].Pinned != alive[j].Pinned {
				return alive[i].Pinned
			}
			return alive[i].TCPms < alive[j].TCPms
		})
		if opt.MaxXray > 0 && len(alive) > opt.MaxXray {
			alive = alive[:opt.MaxXray]
		}
		res.XrayTested = len(alive)
		passed, err := xrayStage(ctx, dir, alive, opt)
		if err != nil {
			return res, fmt.Errorf("xray-knife stage: %w (use --skip-xray for TCP-only)", err)
		}
		alive = passed
	}
	res.Alive = len(alive)

	ok := map[string]bool{}
	for _, c := range alive {
		ok[c.Key] = true
	}
	for _, c := range cands {
		hist.Observe(c.Key, ok[c.Key], now)
	}
	hist.Expire(now)
	if err := hist.Save(HistoryPath(dir)); err != nil {
		return res, err
	}

	// Per-source alive counts → source score.
	if opt.RegistryDir != "" {
		perSrc := map[string]int{}
		for _, c := range alive {
			for _, s := range c.Sources {
				perSrc[s]++
			}
		}
		reg, err := registry.Load(opt.RegistryDir, SourcesStatePath(dir), now)
		if err != nil {
			return res, err
		}
		for _, s := range reg.Sources {
			if s.LastFetch.Equal(now) || now.Sub(s.LastFetch) < 2*time.Hour { // fetched this run
				s.RecordAlive(perSrc[s.URL])
			}
		}
		if err := reg.Save(SourcesStatePath(dir)); err != nil {
			return res, err
		}
	}
	return res, writeJSONL(AlivePath(dir), alive)
}

func streak(h History, k string) int {
	if e := h[k]; e != nil {
		return e.OKStreak
	}
	return 0
}

func tcpStage(ctx context.Context, cands []Candidate, timeout time.Duration, workers int) []Candidate {
	if workers <= 0 {
		workers = 300
	}
	type r struct {
		ok bool
		ip string
		ms int
	}
	out := make([]r, len(cands))
	d := net.Dialer{Timeout: timeout}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				c := cands[i]
				start := time.Now()
				cctx, cancel := context.WithTimeout(ctx, timeout+time.Second)
				conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
				cancel()
				if err != nil {
					continue
				}
				ip := ""
				if ta, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
					ip = ta.IP.String()
				}
				conn.Close()
				ms := int(time.Since(start).Milliseconds())
				if ms < 1 {
					ms = 1
				}
				out[i] = r{true, ip, ms}
			}
		}()
	}
	for i := range cands {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var alive []Candidate
	for i, c := range cands {
		if out[i].ok {
			c.IP, c.TCPms = out[i].ip, out[i].ms
			alive = append(alive, c)
		}
	}
	return alive
}

// xrayStage tests each config through a real xray core using xray-knife:
//
//	xray-knife http -f in.txt -o res.jsonl -x jsonl -t N -d MS --timeout MS -u URL
//	  --rip=true --diagnose=false --dedup-semantic=false --sort=false --quiet
//
// Each input link carries a tag remark (#mapps-<index>) so results map back to candidates
// regardless of how xray-knife orders or re-serialises them.
func xrayStage(ctx context.Context, dir string, cands []Candidate, opt ValidateOptions) ([]Candidate, error) {
	if len(cands) == 0 {
		return nil, nil
	}
	bin, err := exec.LookPath(opt.XrayKnife)
	if err != nil {
		return nil, err
	}
	xkDir, err := filepath.Abs(filepath.Join(dir, "xk"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(xkDir, 0o755); err != nil {
		return nil, err
	}
	in := filepath.Join(xkDir, "in.txt")
	outPath := filepath.Join(xkDir, "res.jsonl")
	_ = os.Remove(outPath)
	var sb strings.Builder
	for i, c := range cands {
		sb.WriteString(c.URI + "#" + tag(i) + "\n")
	}
	if err := os.WriteFile(in, []byte(sb.String()), 0o644); err != nil {
		return nil, err
	}
	args := []string{"http",
		"-f", in, "-o", outPath, "-x", "jsonl",
		"-t", strconv.Itoa(opt.XKThreads),
		"-d", strconv.Itoa(opt.XKTimeoutMs),
		"--timeout", strconv.Itoa(opt.XKTimeoutMs),
		"-u", opt.XKURL,
		"-z", "xray",
		"--rip=" + strconv.FormatBool(opt.XKRealIP), "--diagnose=false", "--dedup-semantic=false", "--sort=false",
		"--quiet",
	}
	// Budget: generous but bounded so a hung core can't eat the whole job.
	budget := time.Duration(len(cands)/max(opt.XKThreads, 1)+2)*time.Duration(opt.XKTimeoutMs)*time.Millisecond*2 + 2*time.Minute
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = xkDir
	cmd.Env = append(os.Environ(), "XRAY_KNIFE_HOME="+xkDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Printf("validate: running %s %s (budget %s)", bin, strings.Join(args, " "), budget.Round(time.Second))
	runErr := cmd.Run()
	results, perr := parseXKResults(outPath)
	if perr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%v; results: %v", runErr, perr)
		}
		return nil, perr
	}
	if runErr != nil {
		log.Printf("validate: xray-knife exited with %v — using the %d results it wrote", runErr, len(results))
	}
	var passed []Candidate
	for _, r := range results {
		if !r.Passed {
			continue
		}
		i, ok := untag(r.Link)
		if !ok || i >= len(cands) {
			continue
		}
		c := cands[i]
		c.DelayMs = r.Delay
		c.ExitIP = r.IP
		c.ExitCC = r.CC
		passed = append(passed, c)
	}
	return passed, nil
}

const tagPrefix = "mapps-"

func tag(i int) string { return tagPrefix + strconv.Itoa(i) }

// untag finds our #mapps-<n> remark in a link (fragment may be percent-encoded or not).
func untag(link string) (int, bool) {
	_, frag, ok := strings.Cut(link, "#")
	if !ok {
		return 0, false
	}
	frag = vless.PercentDecode(frag)
	i := strings.Index(frag, tagPrefix)
	if i < 0 {
		return 0, false
	}
	digits := frag[i+len(tagPrefix):]
	j := 0
	for j < len(digits) && digits[j] >= '0' && digits[j] <= '9' {
		j++
	}
	n, err := strconv.Atoi(digits[:j])
	return n, err == nil
}

// xkResult is the subset of an xray-knife jsonl record we use.
type xkResult struct {
	Link   string
	Passed bool
	Delay  int
	IP     string
	CC     string
}

// parseXKResults reads xray-knife's jsonl output. Field names are matched loosely
// (case-insensitive, several aliases) so minor upstream renames don't break the bot.
func parseXKResults(path string) ([]xkResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []xkResult
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		out = append(out, decodeXK(m))
	}
	return out, sc.Err()
}

func decodeXK(m map[string]any) xkResult {
	lm := map[string]any{}
	for k, v := range m {
		lm[strings.ToLower(k)] = v
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := lm[k].(string); ok && v != "" && v != "null" {
				return v
			}
		}
		return ""
	}
	num := func(keys ...string) int {
		for _, k := range keys {
			switch v := lm[k].(type) {
			case float64:
				return int(v)
			case string:
				if n, err := strconv.Atoi(strings.TrimSuffix(v, "ms")); err == nil {
					return n
				}
			}
		}
		return 0
	}
	r := xkResult{
		Link:  str("link", "config_link", "configlink", "config", "uri"),
		Delay: num("delay", "real_delay", "realdelay", "latency", "delay_ms"),
		IP:    str("ip", "real_ip", "realip", "exit_ip"),
		CC:    str("location", "country", "loc", "cc", "country_code"),
	}
	status := strings.ToLower(str("status", "result", "grade"))
	switch {
	case status != "":
		r.Passed = status == "passed" || status == "ok" || status == "success"
	default:
		if b, ok := lm["passed"].(bool); ok {
			r.Passed = b
		} else {
			r.Passed = r.Delay > 0
		}
	}
	return r
}
