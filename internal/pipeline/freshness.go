package pipeline

import (
	"context"
	"time"

	"github.com/indelingDanil/MyAppVPN-subs/internal/registry"
)

// CommitDater returns the date of the last commit touching a file (gh.GitHub implements it).
type CommitDater interface {
	LastCommit(ctx context.Context, repo, ref, path string) (time.Time, error)
}

// UpdateFreshness sets s.LastUpdated / FreshnessMethod / Stale after a successful fetch.
//
// Preference: (a) GitHub commit date for GitHub-hosted files, (b) Last-Modified header,
// (c) our own body hash (content_changed_at). The commits API is only called when the body
// changed or no GitHub date is cached yet — an unchanged body can't have a newer commit —
// and at most *budget times per run.
func UpdateFreshness(ctx context.Context, s *registry.Source, lastModified time.Time, changed bool, cd CommitDater, budget *int, now time.Time) {
	defer s.EvaluateStale(now)
	if f, ok := registry.GitHubFileRef(s.URL); ok && cd != nil {
		cached := s.FreshnessMethod == registry.MethodGitHub && !s.LastUpdated.IsZero()
		if cached && !changed {
			return
		}
		if *budget > 0 {
			*budget--
			if t, err := cd.LastCommit(ctx, f.Repo, f.Ref, f.Path); err == nil {
				s.SetUpdated(t, registry.MethodGitHub)
				return
			}
		} else if cached {
			// Out of budget but the body changed: it was updated within the last run interval.
			s.SetUpdated(s.ContentChangedAt, registry.MethodGitHub)
			return
		}
	}
	if !lastModified.IsZero() {
		s.SetUpdated(lastModified, registry.MethodLastModified)
		return
	}
	s.UseHashFreshness()
}
