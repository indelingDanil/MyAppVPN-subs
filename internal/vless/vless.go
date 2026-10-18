// Package vless is a strict, side-effect-free parser for vless:// share links.
//
// The acceptance rules mirror the MyAppVPN Android client
// (app/src/main/java/com/myappvpn/data/parser/VlessUriParser.kt) so that every line
// this repo publishes survives the app's own filter:
//
//   - transport (`type`, lowercased, missing = tcp) ∈ {tcp, raw, ws, grpc, httpupgrade, xhttp, splithttp, kcp}
//   - security (lowercased, missing = none) ∈ {tls, reality} — the app also accepts
//     "none", but this repo never publishes unencrypted configs
//   - flow (case-sensitive) ∈ {"", xtls-rprx-vision, xtls-rprx-vision-udp443}
//   - reality requires a non-empty `pbk`
//
// Query parsing intentionally copies the app: keys are taken verbatim, values are
// percent-decoded once (lenient, '+' is NOT a space), the last duplicate key wins.
package vless

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Config is a parsed vless:// link.
type Config struct {
	UUID      string
	Host      string // without IPv6 brackets
	Port      int
	Params    map[string]string // decoded values, raw keys (last duplicate wins)
	Remark    string            // decoded #fragment
	Body      string            // the original link without the #fragment (as found, &amp; unescaped)
	Transport string            // normalized: lowercased, default "tcp"
	Security  string            // normalized: lowercased, default "none"
	Flow      string
}

var (
	// SupportedTransports matches the app's SUPPORTED_TRANSPORTS.
	SupportedTransports = map[string]bool{
		"tcp": true, "raw": true, "ws": true, "grpc": true, "httpupgrade": true,
		"xhttp": true, "splithttp": true, "kcp": true,
	}
	// PublishedSecurity is the subset of the app's SUPPORTED_SECURITY we publish (no "none").
	PublishedSecurity = map[string]bool{"tls": true, "reality": true}
	// SupportedFlows matches the app's SUPPORTED_FLOWS.
	SupportedFlows = map[string]bool{"": true, "xtls-rprx-vision": true, "xtls-rprx-vision-udp443": true}
)

const maxURILen = 4096

var errBad = errors.New("malformed vless uri")

// Parse parses one link. It never performs I/O.
func Parse(raw string) (*Config, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "vless://") || len(s) > maxURILen || !utf8.ValidString(s) {
		return nil, errBad
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return nil, errBad
		}
	}
	c := &Config{}
	body, frag, hasFrag := strings.Cut(s, "#")
	if hasFrag {
		c.Remark = PercentDecode(frag)
	}
	c.Body = body
	rest := body[len("vless://"):]
	at := strings.IndexByte(rest, '@')
	if at <= 0 {
		return nil, errBad
	}
	c.UUID = strings.TrimSpace(rest[:at])
	if c.UUID == "" || len(c.UUID) > 128 || strings.ContainsAny(c.UUID, "/?#[]") {
		return nil, errBad
	}
	rest = rest[at+1:]
	end := len(rest)
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		end = i
	}
	host, port, ok := parseHostPort(rest[:end])
	if !ok {
		return nil, errBad
	}
	c.Host, c.Port = host, port
	query := ""
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		query = rest[q+1:]
	}
	c.Params = parseQuery(query)
	// The app uses `params["type"] ?: "tcp"`: only a MISSING key defaults; an empty value
	// ("type=") stays "" and is then rejected as an unsupported transport. Same for security.
	c.Transport = strings.ToLower(paramOr(c.Params, "type", "tcp"))
	c.Security = strings.ToLower(paramOr(c.Params, "security", "none"))
	c.Flow = c.Params["flow"]
	return c, nil
}

func paramOr(m map[string]string, k, d string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return d
}

// AppAccepts reports whether the MyAppVPN client would keep this config AND it is something
// we are willing to publish (security tls/reality only).
func (c *Config) AppAccepts() bool {
	if !SupportedTransports[c.Transport] {
		return false
	}
	if !PublishedSecurity[c.Security] {
		return false
	}
	if !SupportedFlows[c.Flow] {
		return false
	}
	if c.Security == "reality" && strings.TrimSpace(c.Params["pbk"]) == "" {
		return false
	}
	return true
}

// PublicHost reports whether the host is something worth dialing: a domain name or a
// globally routable IP (no loopback / private / link-local / unspecified).
func (c *Config) PublicHost() bool {
	if a, err := netip.ParseAddr(c.Host); err == nil {
		return a.IsGlobalUnicast() && !a.IsPrivate()
	}
	h := strings.ToLower(strings.TrimSuffix(c.Host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".localhost") || !strings.Contains(h, ".") {
		return false
	}
	return true
}

// IsUUID reports whether s is a canonical 8-4-4-4-12 hex UUID.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHex(byte(r)) {
				return false
			}
		}
	}
	return true
}

// Key is the canonical dedupe identity: uuid@host:port + transport + security + sni + pbk +
// path/serviceName. Remark and query order are ignored; real UUIDs and hosts are lowercased;
// raw≡tcp and splithttp≡xhttp.
func (c *Config) Key() string {
	uuid := c.UUID
	if IsUUID(uuid) {
		uuid = strings.ToLower(uuid)
	}
	host := strings.TrimSuffix(strings.ToLower(c.Host), ".")
	if a, err := netip.ParseAddr(host); err == nil {
		host = a.Unmap().String()
	}
	t := c.Transport
	switch t {
	case "raw":
		t = "tcp"
	case "splithttp":
		t = "xhttp"
	}
	var extra string
	switch t {
	case "grpc":
		extra = c.Params["serviceName"]
	case "tcp", "kcp":
		extra = ""
	default:
		extra = c.Params["path"]
	}
	return strings.Join([]string{
		uuid + "@" + joinHostPort(host, c.Port),
		t, c.Security, strings.ToLower(c.Params["sni"]), c.Params["pbk"], extra,
	}, "|")
}

// HostPort returns a dialable "host:port" (IPv6 bracketed).
func (c *Config) HostPort() string { return joinHostPort(c.Host, c.Port) }

func joinHostPort(h string, p int) string {
	if strings.Contains(h, ":") {
		return "[" + h + "]:" + strconv.Itoa(p)
	}
	return h + ":" + strconv.Itoa(p)
}

// WithRemark returns the original link body with a new, percent-encoded #remark.
func (c *Config) WithRemark(remark string) string {
	return c.Body + "#" + EncodeFragment(remark)
}

// parseHostPort mirrors the app: "[v6]:port", "[v6]", "host:port", "host"; port defaults to
// 443 only when absent; present-but-invalid port → reject. Additionally the host must be a
// syntactically valid IP or DNS name.
func parseHostPort(hp string) (string, int, bool) {
	var host, portStr string
	hasPort := false
	if strings.HasPrefix(hp, "[") {
		cb := strings.IndexByte(hp, ']')
		if cb < 0 {
			return "", 0, false
		}
		host = hp[1:cb]
		after := hp[cb+1:]
		switch {
		case after == "":
		case strings.HasPrefix(after, ":"):
			portStr, hasPort = after[1:], true
		default:
			return "", 0, false
		}
		a, err := netip.ParseAddr(host)
		if err != nil || !a.Is6() {
			return "", 0, false
		}
	} else {
		if i := strings.LastIndexByte(hp, ':'); i >= 0 {
			host, portStr, hasPort = hp[:i], hp[i+1:], true
		} else {
			host = hp
		}
		if !validHostname(host) {
			return "", 0, false
		}
	}
	if strings.TrimSpace(host) == "" {
		return "", 0, false
	}
	port := 443
	if hasPort && portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return "", 0, false
		}
		port = p
	}
	return host, port, true
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			b := label[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				return false
			}
		}
	}
	return true
}

func parseQuery(q string) map[string]string {
	m := map[string]string{}
	if q == "" {
		return m
	}
	for _, p := range strings.Split(q, "&") {
		if p == "" {
			continue
		}
		k, v, _ := strings.Cut(p, "=")
		m[k] = PercentDecode(v)
	}
	return m
}

// PercentDecode is the app's lenient decoder: valid %XX runs that form valid UTF-8 are
// decoded, anything else is kept verbatim, '+' stays '+'.
func PercentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			start := i
			var buf []byte
			for i+2 < len(s) && s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
				buf = append(buf, unhex(s[i+1])<<4|unhex(s[i+2]))
				i += 3
			}
			if utf8.Valid(buf) {
				out.Write(buf)
			} else {
				out.WriteString(s[start:i])
			}
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// EncodeFragment percent-encodes everything except RFC 3986 unreserved characters.
func EncodeFragment(s string) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexd[c>>4])
			b.WriteByte(hexd[c&15])
		}
	}
	return b.String()
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func unhex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}
