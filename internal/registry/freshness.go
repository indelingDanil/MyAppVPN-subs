package registry

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Freshness / availability rules (apply to pinned sources too):
//
//   - HTTP 404/410/451 ("repo/file gone") → the source is excluded from that run and marked
//     dead_since. Dead sources are re-fetched on every collect run and come back on the first
//     200. Non-pinned dead sources are pruned when the weekly discover re-checks them and
//     they are still gone; pinned ones stay in pinned.txt.
//   - Content not updated for more than StaleDays → excluded as stale (stays registered, comes
//     back automatically once updated). Non-pinned sources stale for more than
//     StalePruneDays are pruned.
//   - "Updated" is measured, in order of preference, by (a) the last GitHub commit touching
//     the file, (b) the HTTP Last-Modified header, (c) our own sha256 of the body
//     (content_changed_at) — (c) only judges after ObservedMinDays of observation.
const (
	StaleDays       = 7
	StalePruneDays  = 30
	ObservedMinDays = 7
)

// Freshness methods.
const (
	MethodGitHub       = "github"
	MethodLastModified = "last-modified"
	MethodHash         = "hash"
)

const day = 24 * time.Hour

// MarkGone records a 404/410/451 answer.
func (s *Source) MarkGone(now time.Time, status int) {
	s.LastFetch = now
	if s.DeadSince.IsZero() {
		s.DeadSince = now
	}
	s.DeadStatus = status
}

// Dead reports whether the source is currently in a "gone" outage.
func (s *Source) Dead() bool { return !s.DeadSince.IsZero() }

// ObserveBody records a 200 body's hash. It clears any dead state and returns whether the
// content changed since the previous observation (true on the first observation).
func (s *Source) ObserveBody(now time.Time, hash string) (changed bool) {
	s.DeadSince, s.DeadStatus = time.Time{}, 0
	if s.ObservedSince.IsZero() {
		s.ObservedSince = now
	}
	if hash != s.ContentHash {
		s.ContentHash = hash
		s.ContentChangedAt = now
		return true
	}
	return false
}

// SetUpdated stores an authoritative update time (methods a/b).
func (s *Source) SetUpdated(t time.Time, method string) {
	s.LastUpdated = t.UTC()
	s.FreshnessMethod = method
}

// UseHashFreshness falls back to method (c).
func (s *Source) UseHashFreshness() {
	s.LastUpdated = s.ContentChangedAt
	s.FreshnessMethod = MethodHash
}

// EvaluateStale recomputes and stores Stale.
func (s *Source) EvaluateStale(now time.Time) bool {
	switch {
	case s.LastUpdated.IsZero():
		s.Stale = false
	case s.FreshnessMethod == MethodHash:
		s.Stale = now.Sub(s.ObservedSince) >= ObservedMinDays*day && now.Sub(s.LastUpdated) > StaleDays*day
	default:
		s.Stale = now.Sub(s.LastUpdated) > StaleDays*day
	}
	return s.Stale
}

// StaleDaysAt is the whole number of days since the last update.
func (s *Source) StaleDaysAt(now time.Time) int {
	if s.LastUpdated.IsZero() {
		return 0
	}
	return int(now.Sub(s.LastUpdated) / day)
}

// ExclusionReason returns why this source is excluded from the current run ("" = included).
func (s *Source) ExclusionReason(now time.Time) string {
	if s.Dead() {
		return fmt.Sprintf("%d (gone since %s)", s.DeadStatus, s.DeadSince.Format("2006-01-02"))
	}
	if s.Stale {
		return fmt.Sprintf("stale %dd", s.StaleDaysAt(now))
	}
	return ""
}

// stalePrunable: non-pinned and stale for more than StalePruneDays.
func (s *Source) stalePrunable(now time.Time) bool {
	return !s.Pinned && s.Stale && !s.LastUpdated.IsZero() && now.Sub(s.LastUpdated) > StalePruneDays*day
}

// GitHubFile identifies a file in a GitHub repository.
type GitHubFile struct {
	Repo string // owner/name
	Ref  string // branch/tag/sha; "" = default branch
	Path string
}

// GitHubFileRef maps raw.githubusercontent.com/<o>/<r>/<ref>/<path>,
// cdn.jsdelivr.net/gh/<o>/<r>[@ref]/<path> and github.com/<o>/<r>/(blob|raw)/<ref>/<path>
// to a GitHubFile. Refs containing "/" can't be told apart from the path; the first segment
// is assumed to be the ref (the commits API then fails and we fall back to other methods).
func GitHubFileRef(raw string) (GitHubFile, bool) {
	if n, ok := NormalizeURL(raw); ok {
		raw = n
	}
	u, err := url.Parse(raw)
	if err != nil {
		return GitHubFile{}, false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch u.Host {
	case "raw.githubusercontent.com":
		if len(segs) < 4 {
			return GitHubFile{}, false
		}
		return GitHubFile{segs[0] + "/" + segs[1], segs[2], strings.Join(segs[3:], "/")}, true
	case "cdn.jsdelivr.net":
		if len(segs) < 4 || segs[0] != "gh" {
			return GitHubFile{}, false
		}
		repo, ref, _ := strings.Cut(segs[2], "@")
		return GitHubFile{segs[1] + "/" + repo, ref, strings.Join(segs[3:], "/")}, true
	}
	return GitHubFile{}, false
}
