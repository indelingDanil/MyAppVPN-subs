package registry

import (
	"net/url"
	"regexp"
	"strings"
)

// NormalizeURL canonicalises a subscription URL:
//   - github.com/<o>/<r>/blob|raw/<ref>/<path> → raw.githubusercontent.com/<o>/<r>/<ref>/<path>
//   - raw.githubusercontent.com/<o>/<r>/refs/heads/<ref>/… → …/<o>/<r>/<ref>/…
//   - forces https, lowercases the host, drops #fragments and ?raw=true / ?plain=1.
//
// Only https/http URLs on a plausible host are accepted.
func NormalizeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ".,;:!?)]}>'\"`*")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", false
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	q := u.Query()
	q.Del("raw")
	q.Del("plain")
	u.RawQuery = q.Encode()
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	switch u.Host {
	case "github.com", "www.github.com":
		// /o/r/blob/ref/path… or /o/r/raw/ref/path…
		if len(segs) >= 5 && (segs[2] == "blob" || segs[2] == "raw") {
			rest := segs[3:]
			if len(rest) >= 3 && rest[0] == "refs" && rest[1] == "heads" {
				rest = rest[2:]
			}
			return "https://raw.githubusercontent.com/" + segs[0] + "/" + segs[1] + "/" + strings.Join(rest, "/"), true
		}
		return "", false // repo pages etc. are not subscription files
	case "raw.githubusercontent.com":
		if len(segs) >= 6 && segs[2] == "refs" && segs[3] == "heads" {
			segs = append(segs[:2], segs[4:]...)
		}
		if len(segs) < 4 {
			return "", false
		}
		return "https://raw.githubusercontent.com/" + strings.Join(segs, "/"), true
	}
	if len(segs) == 0 || segs[0] == "" {
		return "", false
	}
	return u.String(), true
}

var urlRe = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `\)\]\}|\\^]+`)

// rawHostRe matches hosts/paths that serve raw files.
func isRawLike(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	p := pu.Path
	switch pu.Host {
	case "raw.githubusercontent.com":
		return true
	case "cdn.jsdelivr.net":
		return strings.HasPrefix(p, "/gh/")
	case "gitlab.com":
		return strings.Contains(p, "/-/raw/")
	case "codeberg.org":
		return strings.Contains(p, "/raw/")
	case "gist.githubusercontent.com":
		return true
	}
	return false
}

// ExtractCandidates finds subscription-looking links in free text (a README). Only raw-file
// hosts are kept; github.com blob/raw links are converted. Image/archive links are skipped.
func ExtractCandidates(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range urlRe.FindAllString(text, -1) {
		n, ok := NormalizeURL(m)
		if !ok || !isRawLike(n) || seen[n] {
			continue
		}
		low := strings.ToLower(n)
		skip := false
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".zip", ".apk", ".exe", ".md", ".html", ".js", ".py", ".sh", ".mp4", ".pdf", ".json", ".yaml", ".yml"} {
			if strings.HasSuffix(low, ext) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// RootFileLooksLikeSub decides whether a repo-root file name is worth probing.
func RootFileLooksLikeSub(name string) bool {
	l := strings.ToLower(name)
	if !strings.HasSuffix(l, ".txt") && strings.Contains(l, ".") {
		return false
	}
	for _, k := range []string{"vless", "sub", "config", "reality", "v2ray", "xray", "mix"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}
