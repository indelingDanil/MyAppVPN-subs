// Package gh is a minimal GitHub REST client (search, readme, trees, commits) that honours rate limits.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/fetch"
)

// GitHub is a minimal REST client that honours rate limits.
type GitHub struct {
	Token   string
	HTTP    *http.Client
	MaxWait time.Duration // longest single rate-limit sleep
	Calls   int
}

// NewGitHub returns a client; token may be empty (60 req/h, 10 search/min).
func NewGitHub(token string) *GitHub {
	return &GitHub{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}, MaxWait: 3 * time.Minute}
}

// ErrNotFound is returned for 404s.
var ErrNotFound = fmt.Errorf("not found")

// get performs GET https://api.github.com<path> and returns the body. accept may be "".
func (g *GitHub) get(ctx context.Context, path, accept string, maxBytes int64) ([]byte, error) {
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
		if err != nil {
			return nil, err
		}
		if accept == "" {
			accept = "application/vnd.github+json"
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", fetch.UserAgent)
		if g.Token != "" {
			req.Header.Set("Authorization", "Bearer "+g.Token)
		}
		g.Calls++
		resp, err := g.HTTP.Do(req)
		if err != nil {
			if attempt < 3 {
				time.Sleep(2 * time.Second)
				continue
			}
			return nil, err
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			return body, rerr
		case resp.StatusCode == http.StatusNotFound:
			return nil, ErrNotFound
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
			wait := rateLimitWait(resp.Header, time.Now())
			if wait > g.MaxWait {
				return nil, fmt.Errorf("rate limited for %s (> max wait)", wait.Round(time.Second))
			}
			log.Printf("github: rate limited on %s, sleeping %s", path, wait.Round(time.Second))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		case resp.StatusCode >= 500:
			time.Sleep(3 * time.Second)
		default:
			return nil, fmt.Errorf("github %s: http %d", path, resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("github %s: giving up", path)
}

// rateLimitWait derives how long to sleep from Retry-After / X-RateLimit-Reset.
func rateLimitWait(h http.Header, now time.Time) time.Duration {
	if ra := h.Get("Retry-After"); ra != "" {
		if s, err := strconv.Atoi(ra); err == nil {
			return time.Duration(s)*time.Second + time.Second
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if rs, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			d := time.Unix(rs, 0).Sub(now) + 2*time.Second
			if d > 0 {
				return d
			}
		}
	}
	return 60 * time.Second // secondary rate limit without hints: GitHub asks for ≥1 min
}

// Repo is the subset of a search result we use.
type Repo struct {
	FullName      string    `json:"full_name"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	Archived      bool      `json:"archived"`
	Fork          bool      `json:"fork"`
	Stars         int       `json:"stargazers_count"`
}

// SearchRepos runs one /search/repositories query, sorted by updated, up to pages×perPage.
func (g *GitHub) SearchRepos(ctx context.Context, q string, pages, perPage int) ([]Repo, error) {
	var out []Repo
	for p := 1; p <= pages; p++ {
		v := url.Values{"q": {q}, "sort": {"updated"}, "order": {"desc"}, "per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(p)}}
		b, err := g.get(ctx, "/search/repositories?"+v.Encode(), "", 4<<20)
		if err != nil {
			return out, err
		}
		var res struct {
			Items []Repo `json:"items"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return out, err
		}
		out = append(out, res.Items...)
		if len(res.Items) < perPage {
			break
		}
		// search API: 30 req/min authenticated — stay well below.
		time.Sleep(2500 * time.Millisecond)
	}
	return out, nil
}

// Readme returns the raw README text (capped at 512 KiB).
func (g *GitHub) Readme(ctx context.Context, fullName string) (string, error) {
	b, err := g.get(ctx, "/repos/"+fullName+"/readme", "application/vnd.github.raw+json", 512<<10)
	return string(b), err
}

// RootFiles lists blob names in the root tree of ref (one API call).
func (g *GitHub) RootFiles(ctx context.Context, fullName, ref string) ([]string, error) {
	b, err := g.get(ctx, "/repos/"+fullName+"/git/trees/"+url.PathEscape(ref), "", 4<<20)
	if err != nil {
		return nil, err
	}
	var res struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, t := range res.Tree {
		if t.Type == "blob" && t.Size > 0 && t.Size <= fetch.MaxBody*4 && !strings.Contains(t.Path, "/") {
			out = append(out, t.Path)
		}
	}
	return out, nil
}

// LastCommit returns the committer date of the newest commit touching path on ref
// (GET /repos/{o}/{r}/commits?path=…&sha=…&per_page=1). ref may be "" (default branch).
func (g *GitHub) LastCommit(ctx context.Context, fullName, ref, path string) (time.Time, error) {
	v := url.Values{"path": {path}, "per_page": {"1"}}
	if ref != "" {
		v.Set("sha", ref)
	}
	b, err := g.get(ctx, "/repos/"+fullName+"/commits?"+v.Encode(), "", 1<<20)
	if err != nil {
		return time.Time{}, err
	}
	var res []struct {
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return time.Time{}, err
	}
	if len(res) == 0 || res[0].Commit.Committer.Date.IsZero() {
		return time.Time{}, ErrNotFound
	}
	return res[0].Commit.Committer.Date.UTC(), nil
}
