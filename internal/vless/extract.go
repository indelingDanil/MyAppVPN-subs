package vless

import (
	"encoding/base64"
	"html"
	"strings"
)

// proxySchemes are link prefixes counted as "proxy-looking" when judging a source.
var proxySchemes = []string{"vless://", "vmess://", "trojan://", "ss://", "ssr://", "hysteria2://", "hy2://", "hysteria://", "tuic://", "wireguard://"}

// DecodeBody returns the plain-text form of a subscription body: as-is when it already
// contains share links, otherwise the first successful base64 decoding (standard / raw /
// URL-safe / raw URL-safe, whitespace ignored) that yields share links. Bodies that are
// neither are returned unchanged.
func DecodeBody(body []byte) string {
	s := string(body)
	if containsProxyLink(s) {
		return s
	}
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if cleaned == "" {
		return s
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		in := cleaned
		if enc == base64.RawStdEncoding || enc == base64.RawURLEncoding {
			in = strings.TrimRight(in, "=")
		}
		if b, err := enc.DecodeString(in); err == nil && containsProxyLink(string(b)) {
			return string(b)
		}
	}
	return s
}

func containsProxyLink(s string) bool {
	for _, p := range proxySchemes {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// ExtractURIs pulls every vless:// link out of decoded text. Links are cut at whitespace,
// quotes, angle brackets and backticks; HTML entities (&amp; …) are unescaped.
func ExtractURIs(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "vless://") {
			continue
		}
		if strings.Contains(line, "&") {
			line = html.UnescapeString(line)
		}
		for {
			i := strings.Index(line, "vless://")
			if i < 0 {
				break
			}
			line = line[i:]
			end := strings.IndexAny(line, " \t\r\"'<>`")
			if j := strings.Index(line[len("vless://"):], "vless://"); j >= 0 && (end < 0 || j+len("vless://") < end) {
				end = j + len("vless://")
			}
			if end < 0 {
				end = len(line)
			}
			out = append(out, strings.TrimRight(line[:end], ",;"))
			line = line[end:]
		}
	}
	return out
}

// Stats summarises a body for source acceptance.
type Stats struct {
	NonEmpty   int // non-empty, non-comment lines in the decoded body
	ProxyLines int // lines that start with a known proxy scheme
	VlessLinks int // vless:// links found
	Valid      int // vless links that parse and pass AppAccepts (not deduped)
}

// Analyze decodes a body and counts what is in it.
func Analyze(body []byte) (Stats, []*Config) {
	text := DecodeBody(body)
	var st Stats
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "//") {
			continue
		}
		st.NonEmpty++
		for _, p := range proxySchemes {
			if strings.HasPrefix(l, p) {
				st.ProxyLines++
				break
			}
		}
	}
	var cfgs []*Config
	for _, u := range ExtractURIs(text) {
		st.VlessLinks++
		c, err := Parse(u)
		if err != nil || !c.AppAccepts() || !c.PublicHost() {
			continue
		}
		st.Valid++
		cfgs = append(cfgs, c)
	}
	return st, cfgs
}

// LooksLikeSubscription is the discovery acceptance rule: ≥20 valid vless lines, or ≥50%
// proxy-looking lines (with at least a handful of lines and at least one vless link).
func (s Stats) LooksLikeSubscription() bool {
	if s.Valid >= 20 {
		return true
	}
	return s.NonEmpty >= 5 && s.VlessLinks > 0 && s.ProxyLines*2 >= s.NonEmpty
}
