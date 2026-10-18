package pipeline

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/geo"
	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
	"github.com/indelingDanil/MyAppVPN-subs/internal/vless"
)

// PublishOptions tune publish.
type PublishOptions struct {
	OutDir            string
	MMDB              string
	MaxPerFile        int
	PerCountry        int // cap per country in vless.txt
	PerCountryReality int // cap per country in reality.txt
	MinTotal          int
	SourcesUsed       int
}

// PublishResult summarises a publish run.
type PublishResult struct {
	Total, Reality, TLS, RealityFile int
	ByCountry                        map[string]int
}

// MaxFileBytes keeps every published file under the app's 1 MB limit.
const MaxFileBytes = 1000 * 1000

// Countries never published in the main files: UA (dropped by the app anyway) and RU (an RU
// exit is useless for bypassing RU blocks). RU still gets by-country/RU.txt (whitelist use).
var excludedMain = map[string]bool{"UA": true, "RU": true}

// excludedEverywhere are countries without a by-country file either.
var excludedEverywhere = map[string]bool{"UA": true}

type ranked struct {
	Candidate
	CC     string
	Streak int
	Line   string
}

// Rank orders configs: reality before tls, then ok_streak desc, then delay asc
// (xray delay when known, TCP connect time otherwise), then key for determinism.
func Rank(items []ranked) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if (a.Security == "reality") != (b.Security == "reality") {
			return a.Security == "reality"
		}
		if a.Streak != b.Streak {
			return a.Streak > b.Streak
		}
		da, db := a.delay(), b.delay()
		if da != db {
			return da < db
		}
		return a.Key < b.Key
	})
}

func (r ranked) delay() int {
	if r.DelayMs > 0 {
		return r.DelayMs
	}
	if r.TCPms > 0 {
		return 10000 + r.TCPms // TCP-only results sort after real-delay ones
	}
	return 1 << 30
}

// Publish writes the output tree (see README for the file contract).
func Publish(dir string, opt PublishOptions, now time.Time) (PublishResult, error) {
	res := PublishResult{ByCountry: map[string]int{}}
	alive, err := readJSONL(AlivePath(dir))
	if err != nil {
		return res, fmt.Errorf("read alive (run validate first): %w", err)
	}
	hist, err := LoadHistory(HistoryPath(dir))
	if err != nil {
		return res, err
	}
	db, err := geo.Open(opt.MMDB)
	if err != nil {
		return res, fmt.Errorf("open mmdb: %w", err)
	}
	defer db.Close()

	var items []ranked
	for _, c := range alive {
		cfg, err := vless.Parse(c.URI)
		if err != nil || !cfg.AppAccepts() { // defence in depth: never publish what the app drops
			continue
		}
		cc := geo.NormalizeCC(c.ExitCC)
		if cc == geo.Unknown && c.ExitIP != "" {
			cc = db.Country(c.ExitIP)
		}
		if cc == geo.Unknown && c.IP != "" {
			cc = db.Country(c.IP)
		}
		if cc == geo.Unknown {
			cc = db.Country(c.Host) // literal IP hosts
		}
		items = append(items, ranked{Candidate: c, CC: cc, Streak: streak(hist, c.Key)})
	}
	Rank(items)

	// Stable-per-run numbering per security, in rank order, shared by every file.
	counters := map[string]int{}
	for i := range items {
		counters[items[i].Security]++
		cfg, _ := vless.Parse(items[i].URI)
		items[i].Line = cfg.WithRemark(geo.Remark(items[i].CC, items[i].Security, counters[items[i].Security]))
	}

	var main, reality []ranked
	perCC, perCCR := map[string]int{}, map[string]int{}
	byCountry := map[string][]ranked{}
	for _, it := range items {
		if !excludedEverywhere[it.CC] && len(byCountry[it.CC]) < opt.MaxPerFile {
			byCountry[it.CC] = append(byCountry[it.CC], it)
		}
		if excludedMain[it.CC] {
			continue
		}
		if len(main) < opt.MaxPerFile && perCC[it.CC] < opt.PerCountry {
			main = append(main, it)
			perCC[it.CC]++
		}
		if it.Security == "reality" && len(reality) < opt.MaxPerFile && perCCR[it.CC] < opt.PerCountryReality {
			reality = append(reality, it)
			perCCR[it.CC]++
		}
	}
	if len(main) < opt.MinTotal {
		return res, fmt.Errorf("only %d configs for vless.txt (< --min-total %d); refusing to overwrite the published lists", len(main), opt.MinTotal)
	}

	out := opt.OutDir
	if err := os.RemoveAll(filepath.Join(out, "by-country")); err != nil {
		return res, err
	}
	if err := os.MkdirAll(filepath.Join(out, "by-country"), 0o755); err != nil {
		return res, err
	}
	if err := writeList(out, "vless", main); err != nil {
		return res, err
	}
	if err := writeList(out, "reality", reality); err != nil {
		return res, err
	}
	ccs := make([]string, 0, len(byCountry))
	for cc, list := range byCountry {
		if err := writeText(filepath.Join(out, "by-country", cc+".txt"), lines(list)); err != nil {
			return res, err
		}
		res.ByCountry[cc] = len(list)
		ccs = append(ccs, cc)
	}
	sort.Strings(ccs)

	for _, it := range main {
		if it.Security == "reality" {
			res.Reality++
		} else {
			res.TLS++
		}
	}
	res.Total, res.RealityFile = len(main), len(reality)

	excluded := readExcluded(dir)
	sourcesUsed := opt.SourcesUsed
	if sourcesUsed < 0 {
		sourcesUsed = countSourcesUsed(dir, now)
	}
	meta := map[string]any{
		"schema":     1,
		"updated_at": now.Format(time.RFC3339),
		"counts": map[string]any{
			"total":      res.Total,
			"reality":    res.Reality,
			"tls":        res.TLS,
			"by_country": res.ByCountry,
		},
		"sources_used": sourcesUsed,
		"files": map[string]int{
			"vless.txt":   len(main),
			"reality.txt": len(reality),
		},
		"alive_total": len(items),
		"excluded":    excluded,
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "meta.json"), append(mb, '\n'), 0o644); err != nil {
		return res, err
	}
	if err := os.WriteFile(filepath.Join(out, "stats.md"), []byte(statsMD(res, len(reality), len(items), sourcesUsed, ccs, perCC, excluded, now)), 0o644); err != nil {
		return res, err
	}
	if err := os.WriteFile(filepath.Join(out, "README.md"), []byte(outputReadme), 0o644); err != nil {
		return res, err
	}
	// Carry state to the output branch so the next run (and weekly discover) can read it.
	for _, p := range []string{SourcesStatePath(dir), HistoryPath(dir)} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		dst := filepath.Join(out, "state", filepath.Base(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return res, err
		}
	}
	return res, nil
}

func lines(list []ranked) []string {
	out := make([]string, len(list))
	for i, it := range list {
		out[i] = it.Line
	}
	return out
}

// writeList writes <name>.txt and <name>.b64.
func writeList(out, name string, list []ranked) error {
	ls := lines(list)
	if err := writeText(filepath.Join(out, name+".txt"), ls); err != nil {
		return err
	}
	// b64 is ~4/3 of the text; trim further if needed.
	for len(ls) > 0 && base64.StdEncoding.EncodedLen(len(strings.Join(ls, "\n"))+1) > MaxFileBytes {
		ls = ls[:len(ls)-1]
	}
	enc := base64.StdEncoding.EncodeToString([]byte(strings.Join(ls, "\n") + "\n"))
	return os.WriteFile(filepath.Join(out, name+".b64"), []byte(enc+"\n"), 0o644)
}

func writeText(path string, ls []string) error {
	for len(ls) > 0 && len(strings.Join(ls, "\n"))+1 > MaxFileBytes {
		ls = ls[:len(ls)-1]
	}
	body := strings.Join(ls, "\n")
	if body != "" {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func countSourcesUsed(dir string, now time.Time) int {
	var srcs []registry.Source
	b, err := os.ReadFile(SourcesStatePath(dir))
	if err != nil || json.Unmarshal(b, &srcs) != nil {
		return 0
	}
	n := 0
	for _, s := range srcs {
		if !s.LastOK.IsZero() && now.Sub(s.LastOK) < 6*time.Hour && s.YieldValid > 0 {
			n++
		}
	}
	return n
}

func statsMD(res PublishResult, realityFile, alive, sources int, ccs []string, perCC map[string]int, excluded []Excluded, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# MyAppVPN-subs — stats\n\nUpdated: **%s**\n\n", now.Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| vless.txt | %d (reality %d, tls %d) |\n", res.Total, res.Reality, res.TLS)
	fmt.Fprintf(&b, "| reality.txt | %d |\n", realityFile)
	fmt.Fprintf(&b, "| alive (all countries) | %d |\n", alive)
	fmt.Fprintf(&b, "| sources used | %d |\n\n", sources)
	fmt.Fprintf(&b, "| Country | by-country file | in vless.txt |\n|---|---:|---:|\n")
	for _, cc := range ccs {
		fmt.Fprintf(&b, "| %s %s | %d | %d |\n", geo.Flag(cc), cc, res.ByCountry[cc], perCC[cc])
	}
	if len(excluded) > 0 {
		fmt.Fprintf(&b, "\n## Excluded sources (%d)\n\n| Source | Reason |\n|---|---|\n", len(excluded))
		for _, e := range excluded {
			pin := ""
			if e.Pinned {
				pin = " (pinned)"
			}
			fmt.Fprintf(&b, "| %s%s | %s |\n", e.URL, pin, e.Reason)
		}
	}
	return b.String()
}

const outputReadme = `# MyAppVPN-subs — output

Auto-generated branch (force-pushed every run, history is not kept).
Source code, docs and disclaimer: https://github.com/indelingDanil/MyAppVPN-subs (branch ` + "`main`" + `).

Files: vless.txt / vless.b64 (main list), reality.txt / reality.b64, by-country/<CC>.txt, meta.json, stats.md.
`
