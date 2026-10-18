// Command subs discovers, collects, validates and publishes public VLESS subscriptions for
// the MyAppVPN Android client.
//
//	subs discover --registry registry [--state work/state/sources.json]
//	subs collect  --dir work [--registry registry] [--pinned-only]
//	subs validate --dir work [--xray-knife ./xray-knife | --skip-xray]
//	subs publish  --dir work [--out work/out] [--mmdb GeoLite2-Country.mmdb]
//	subs check    --out work/out   (verify the published tree against the app contract)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/discover"
	"github.com/indelingDanil/MyAppVPN-subs/internal/fetch"
	"github.com/indelingDanil/MyAppVPN-subs/internal/gh"
	"github.com/indelingDanil/MyAppVPN-subs/internal/pipeline"
	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: subs <discover|collect|validate|publish|check> [flags]  (subs <stage> -h for flags)")
	os.Exit(2)
}

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	now := time.Now().UTC().Truncate(time.Second)
	stage, args := os.Args[1], os.Args[2:]
	var err error
	switch stage {
	case "discover":
		err = runDiscover(ctx, args, now)
	case "collect":
		err = runCollect(ctx, args, now)
	case "validate":
		err = runValidate(ctx, args, now)
	case "publish":
		err = runPublish(args, now)
	case "check":
		fs := flag.NewFlagSet("check", flag.ExitOnError)
		out := fs.String("out", "work/out", "published directory to verify")
		_ = fs.Parse(args)
		if err = pipeline.Check(*out, 500); err == nil {
			log.Printf("check: %s OK", *out)
		}
	default:
		usage()
	}
	if err != nil {
		log.Fatalf("%s: %v", stage, err)
	}
}

func runDiscover(ctx context.Context, args []string, now time.Time) error {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	regDir := fs.String("registry", "registry", "registry directory (sources.json, pinned.txt, blocklist.txt)")
	state := fs.String("state", "", "newer per-source stats to merge first (state/sources.json from the output branch)")
	opt := discover.DefaultOptions()
	fs.IntVar(&opt.Days, "days", opt.Days, "only repos pushed within N days")
	fs.IntVar(&opt.Pages, "pages", opt.Pages, "search pages per query")
	fs.IntVar(&opt.PerPage, "per-page", opt.PerPage, "search results per page")
	fs.IntVar(&opt.MaxRepos, "max-repos", opt.MaxRepos, "repos inspected per run")
	fs.IntVar(&opt.MaxProbes, "max-probes", opt.MaxProbes, "candidate URLs fetched per run")
	fs.IntVar(&opt.MaxRootProbes, "max-tree-calls", opt.MaxRootProbes, "git trees API calls per run")
	noSearch := fs.Bool("prune-only", false, "merge stats and prune, skip GitHub search")
	_ = fs.Parse(args)

	reg, err := registry.Load(*regDir, *state, now)
	if err != nil {
		return err
	}
	gone, revived := discover.RecheckDead(ctx, reg, fetch.New(), now)
	log.Printf("discover: re-checked dead sources: %d still gone (removed), %d revived", len(gone), len(revived))
	for _, u := range gone {
		log.Printf("  - %s (still gone)", u)
	}
	removed := reg.Prune(now)
	log.Printf("discover: pruned %d sources", len(removed))
	for _, u := range removed {
		log.Printf("  - %s", u)
	}
	if !*noSearch {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			log.Printf("discover: GITHUB_TOKEN not set — unauthenticated (10 search/min, 60 req/h); use small caps")
		}
		client := gh.NewGitHub(token)
		res := discover.Run(ctx, client, reg, opt, now)
		log.Printf("discover: repos=%d candidates=%d probed=%d accepted=%d added=%d api_calls=%d",
			res.Repos, res.Candidates, res.Probed, res.Accepted, len(res.Added), client.Calls)
		for _, u := range res.Added {
			log.Printf("  + %s", u)
		}
		reg.Prune(now) // enforce the 300 cap after additions
	}
	return reg.SaveTo(*regDir)
}

func runCollect(ctx context.Context, args []string, now time.Time) error {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	dir := fs.String("dir", "work", "work directory")
	var opt pipeline.CollectOptions
	fs.StringVar(&opt.RegistryDir, "registry", "registry", "registry directory")
	fs.IntVar(&opt.MaxCandidates, "max", 20000, "hard cap on unique candidates")
	fs.IntVar(&opt.Workers, "workers", 16, "concurrent source fetches")
	fs.BoolVar(&opt.PinnedOnly, "pinned-only", false, "only fetch registry/pinned.txt sources")
	fs.IntVar(&opt.FreshnessCalls, "freshness-calls", 200, "max GitHub commits-API calls per run for source freshness (20 without GITHUB_TOKEN)")
	_ = fs.Parse(args)
	res, err := pipeline.Collect(ctx, *dir, opt, now)
	if err != nil {
		return err
	}
	log.Printf("collect: sources=%d ok=%d excluded=%d commit_api_calls=%d links=%d valid(per-source unique)=%d unique=%d",
		res.Sources, res.SourcesOK, len(res.Excluded), res.CommitCalls, res.Links, res.Valid, res.Unique)
	for _, e := range res.Excluded {
		log.Printf("  excluded: %s — %s", e.URL, e.Reason)
	}
	return nil
}

func runValidate(ctx context.Context, args []string, now time.Time) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dir := fs.String("dir", "work", "work directory")
	var opt pipeline.ValidateOptions
	fs.StringVar(&opt.RegistryDir, "registry", "registry", "registry directory (for per-source alive stats)")
	fs.DurationVar(&opt.TCPTimeout, "tcp-timeout", 2*time.Second, "TCP connect timeout")
	fs.IntVar(&opt.TCPWorkers, "tcp-workers", 300, "concurrent TCP probes")
	fs.BoolVar(&opt.SkipXray, "skip-xray", false, "skip the xray-knife real-delay stage (keep TCP-alive configs)")
	fs.StringVar(&opt.XrayKnife, "xray-knife", "xray-knife", "path to the xray-knife binary")
	fs.IntVar(&opt.XKThreads, "xk-threads", 100, "xray-knife threads")
	fs.IntVar(&opt.XKTimeoutMs, "xk-timeout", 8000, "xray-knife max delay / timeout (ms)")
	fs.StringVar(&opt.XKURL, "xk-url", "https://www.gstatic.com/generate_204", "URL tested through each config")
	fs.IntVar(&opt.MaxXray, "max-xray", 6000, "test at most N TCP-alive configs with xray-knife (best first)")
	fs.BoolVar(&opt.XKRealIP, "xk-rip", true, "let xray-knife report the egress IP/country (used for the remark)")
	_ = fs.Parse(args)
	res, err := pipeline.Validate(ctx, *dir, opt, now)
	if err != nil {
		return err
	}
	log.Printf("validate: candidates=%d tcp_alive=%d xray_tested=%d alive=%d", res.Candidates, res.TCPAlive, res.XrayTested, res.Alive)
	return nil
}

func runPublish(args []string, now time.Time) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	dir := fs.String("dir", "work", "work directory")
	var opt pipeline.PublishOptions
	fs.StringVar(&opt.OutDir, "out", "", "output directory (default <dir>/out)")
	fs.StringVar(&opt.MMDB, "mmdb", "", "country mmdb (GeoLite2-Country / DB-IP lite); empty = all unknown")
	fs.IntVar(&opt.MaxPerFile, "max-lines", 500, "max lines per file")
	fs.IntVar(&opt.PerCountry, "per-country", 40, "max configs per country in vless.txt")
	fs.IntVar(&opt.PerCountryReality, "per-country-reality", 60, "max configs per country in reality.txt")
	fs.IntVar(&opt.MinTotal, "min-total", 20, "refuse to publish when fewer configs survive (keeps the previous output)")
	fs.IntVar(&opt.SourcesUsed, "sources-used", -1, "override meta.json sources_used (default: from state)")
	_ = fs.Parse(args)
	if opt.OutDir == "" {
		opt.OutDir = *dir + "/out"
	}
	res, err := pipeline.Publish(*dir, opt, now)
	if err != nil {
		return err
	}
	log.Printf("publish: vless.txt=%d (reality=%d tls=%d) reality.txt=%d countries=%d → %s",
		res.Total, res.Reality, res.TLS, res.RealityFile, len(res.ByCountry), opt.OutDir)
	return nil
}
