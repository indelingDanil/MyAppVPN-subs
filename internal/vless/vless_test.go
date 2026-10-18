package vless

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

const (
	uuid    = "4cb9157c-cd6f-451f-b786-1c92edec32cb"
	reality = "vless://" + uuid + "@1.2.3.4:443?security=reality&sni=www.example.com&fp=chrome&pbk=AbCdEf123&sid=ab12&flow=xtls-rprx-vision&type=tcp#old%20name"
	tlsWS   = "vless://" + uuid + "@cdn.example.org:2087?security=tls&sni=cdn.example.org&type=ws&path=%2Fws%3Fed%3D2048&host=cdn.example.org#x"
)

func mustParse(t *testing.T, s string) *Config {
	t.Helper()
	c, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return c
}

func TestParseBasics(t *testing.T) {
	c := mustParse(t, reality)
	if c.UUID != uuid || c.Host != "1.2.3.4" || c.Port != 443 {
		t.Fatalf("got %+v", c)
	}
	if c.Security != "reality" || c.Transport != "tcp" || c.Flow != "xtls-rprx-vision" || c.Params["pbk"] != "AbCdEf123" {
		t.Fatalf("params %+v", c)
	}
	if c.Remark != "old name" {
		t.Fatalf("remark %q", c.Remark)
	}
	w := mustParse(t, tlsWS)
	if w.Params["path"] != "/ws?ed=2048" {
		t.Fatalf("path not decoded: %q", w.Params["path"])
	}
}

func TestParseHostPort(t *testing.T) {
	cases := []struct {
		in       string
		host     string
		port     int
		wantFail bool
	}{
		{"vless://u@[2001:db8::1]:8443?security=tls", "2001:db8::1", 8443, false},
		{"vless://u@[2001:db8::1]?security=tls", "2001:db8::1", 443, false},
		{"vless://u@example.com?security=tls", "example.com", 443, false},
		{"vless://u@example.com:8443/?type=ws&security=tls", "example.com", 8443, false},
		{"vless://u@example.com:0?security=tls", "", 0, true},
		{"vless://u@example.com:70000?security=tls", "", 0, true},
		{"vless://u@example.com:abc?security=tls", "", 0, true},
		{"vless://u@[2001:db8::1?security=tls", "", 0, true},
		{"vless://@example.com:443", "", 0, true},
		{"vless://u@exa mple.com:443", "", 0, true},
		{"vless://u@exa$mple.com:443", "", 0, true},
		{"VLESS://u@example.com:443", "", 0, true}, // app is case-sensitive on the scheme
		{"vmess://u@example.com:443", "", 0, true},
	}
	for _, tc := range cases {
		c, err := Parse(tc.in)
		if tc.wantFail {
			if err == nil {
				t.Errorf("%q: expected failure, got %+v", tc.in, c)
			}
			continue
		}
		if err != nil || c.Host != tc.host || c.Port != tc.port {
			t.Errorf("%q: got %v %+v", tc.in, err, c)
		}
	}
}

// The app's VlessUriParser.sanitizeForXray rules (+ our "no security=none" rule).
func TestAppFilter(t *testing.T) {
	base := "vless://" + uuid + "@example.com:443?"
	cases := map[string]bool{
		"security=tls":                                      true,
		"security=TLS&type=WS":                              true, // lowercased like the app
		"security=reality&pbk=k":                            true,
		"security=reality":                                  false, // reality without pbk
		"security=reality&pbk=":                             false,
		"security=none":                                     false, // app accepts, we never publish
		"":                                                  false, // missing security = none
		"security=xtls":                                     false,
		"security=tls&type=raw":                             true,
		"security=tls&type=ws":                              true,
		"security=tls&type=grpc&serviceName=s":              true,
		"security=tls&type=httpupgrade":                     true,
		"security=tls&type=xhttp":                           true,
		"security=tls&type=splithttp":                       true,
		"security=tls&type=kcp":                             true,
		"security=tls&type=h2":                              false, // removed transport
		"security=tls&type=quic":                            false,
		"security=tls&type=":                                false, // present-but-empty → "" (app semantics)
		"security=tls&flow=xtls-rprx-vision":                true,
		"security=tls&flow=xtls-rprx-vision-udp443":         true,
		"security=tls&flow=":                                true,
		"security=tls&flow=xtls-rprx-direct":                false,
		"security=tls&flow=XTLS-RPRX-VISION":                false, // flow is case-sensitive in the app
		"security=reality&pbk=k&flow=xtls-rprx-vision":      true,
		"security=tls&security=none":                        false, // last duplicate wins
		"security=none&security=tls":                        true,
		"security=reality&pbk=k&sid=&spx=%2F&fp=unknownfp1": true, // unknown fp is not a reject
	}
	for q, want := range cases {
		c, err := Parse(base + q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if got := c.AppAccepts(); got != want {
			t.Errorf("%q: AppAccepts=%v want %v", q, got, want)
		}
	}
}

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"1.2.3.4": true, "example.com": true, "127.0.0.1": false, "10.1.2.3": false,
		"192.168.1.1": false, "0.0.0.0": false, "localhost": false, "[::1]": false, "[2606:4700::1]": true, "intranet": false,
	} {
		c := mustParse(t, "vless://u@"+host+":443?security=tls")
		if got := c.PublicHost(); got != want {
			t.Errorf("%s: PublicHost=%v want %v", host, got, want)
		}
	}
}

func TestCanonicalKeyDedupe(t *testing.T) {
	a := mustParse(t, "vless://"+uuid+"@Example.COM:443?type=ws&security=tls&sni=a.com&path=%2Fp&fp=chrome#one")
	b := mustParse(t, "vless://"+strings.ToUpper(uuid)+"@example.com:443?fp=firefox&path=/p&sni=A.com&security=tls&type=ws#two")
	if a.Key() != b.Key() {
		t.Fatalf("expected same key:\n%s\n%s", a.Key(), b.Key())
	}
	// raw ≡ tcp, and tcp ignores path
	r1 := mustParse(t, "vless://"+uuid+"@h.com:443?type=raw&security=reality&pbk=k&sni=s")
	r2 := mustParse(t, "vless://"+uuid+"@h.com:443?security=reality&pbk=k&sni=s&path=/zzz")
	if r1.Key() != r2.Key() {
		t.Fatalf("raw/tcp keys differ: %s vs %s", r1.Key(), r2.Key())
	}
	for _, other := range []string{
		"vless://" + uuid + "@example.com:8443?type=ws&security=tls&sni=a.com&path=%2Fp", // port
		"vless://" + uuid + "@example.com:443?type=ws&security=tls&sni=b.com&path=%2Fp",  // sni
		"vless://" + uuid + "@example.com:443?type=ws&security=tls&sni=a.com&path=%2Fq",  // path
		"vless://" + uuid + "@example.com:443?type=grpc&security=tls&sni=a.com&serviceName=p",
		"vless://11111111-cd6f-451f-b786-1c92edec32cb@example.com:443?type=ws&security=tls&sni=a.com&path=%2Fp",
	} {
		if mustParse(t, other).Key() == a.Key() {
			t.Errorf("unexpected key collision for %s", other)
		}
	}
	p1 := mustParse(t, "vless://"+uuid+"@h.com:443?security=reality&pbk=k1&sni=s")
	p2 := mustParse(t, "vless://"+uuid+"@h.com:443?security=reality&pbk=k2&sni=s")
	if p1.Key() == p2.Key() {
		t.Error("pbk must be part of the key")
	}
}

func TestWithRemarkRoundTrip(t *testing.T) {
	c := mustParse(t, reality)
	out := c.WithRemark("🇩🇪 DE reality-0012")
	if strings.Contains(out, "old") || !strings.HasPrefix(out, c.Body+"#") {
		t.Fatalf("bad rewrite: %s", out)
	}
	c2 := mustParse(t, out)
	if c2.Remark != "🇩🇪 DE reality-0012" {
		t.Fatalf("remark round-trip: %q", c2.Remark)
	}
	if c2.Key() != c.Key() || !c2.AppAccepts() {
		t.Fatal("rewrite changed identity or acceptance")
	}
}

func TestPercentDecodeLenient(t *testing.T) {
	for in, want := range map[string]string{
		"a%20b":                    "a b",
		"100% fast":                "100% fast",
		"a+b":                      "a+b",
		"%F0%9F%87":                "%F0%9F%87", // incomplete UTF-8 run stays raw
		"%F0%9F%87%A9%F0%9F%87%AA": "🇩🇪",
		"%zz":                      "%zz",
		"end%2":                    "end%2",
	} {
		if got := PercentDecode(in); got != want {
			t.Errorf("PercentDecode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDecodeBodyVariants(t *testing.T) {
	plain := reality + "\n" + tlsWS + "\n"
	std := base64.StdEncoding.EncodeToString([]byte(plain))
	raw := base64.RawStdEncoding.EncodeToString([]byte(plain))
	url := base64.URLEncoding.EncodeToString([]byte(plain))
	rawURL := base64.RawURLEncoding.EncodeToString([]byte(plain))
	wrapped := ""
	for i := 0; i < len(std); i += 76 {
		end := min(i+76, len(std))
		wrapped += std[i:end] + "\r\n"
	}
	for name, body := range map[string]string{
		"plain": plain, "std": std, "raw": raw, "url": url, "rawurl": rawURL, "wrapped": wrapped,
		"padded-spaces": "  " + std + "  \n",
	} {
		got := DecodeBody([]byte(body))
		if us := ExtractURIs(got); len(us) != 2 {
			t.Errorf("%s: extracted %d links from %q", name, len(us), got)
		}
	}
	if got := DecodeBody([]byte("not base64 !!!")); got != "not base64 !!!" {
		t.Errorf("garbage changed: %q", got)
	}
}

func TestExtractURIs(t *testing.T) {
	text := `<a href="` + strings.ReplaceAll(tlsWS, "&", "&amp;") + `">x</a>
` + "`" + reality + "`" + ` and vless://` + uuid + `@h.com:443?security=tls,
two on one line: vless://a@b.com:1?security=tls vless://c@d.com:2?security=tls
vless://x@y.com:3?security=tlsvless://z@w.com:4?security=tls`
	us := ExtractURIs(text)
	if len(us) != 7 {
		t.Fatalf("got %d: %q", len(us), us)
	}
	if us[0] != tlsWS {
		t.Errorf("&amp; not unescaped: %s", us[0])
	}
	if strings.HasSuffix(us[2], ",") {
		t.Errorf("trailing comma kept: %s", us[2])
	}
	if us[5] != "vless://x@y.com:3?security=tls" {
		t.Errorf("glued links not split: %s", us[5])
	}
}

func TestAnalyzeAndAcceptance(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 25; i++ {
		b.WriteString(strings.Replace(reality, "1.2.3.4", "1.2.3."+strconv.Itoa(i+10), 1) + "\n")
	}
	st, cfgs := Analyze([]byte(b.String()))
	if st.Valid != 25 || len(cfgs) != 25 || !st.LooksLikeSubscription() {
		t.Fatalf("stats %+v", st)
	}
	// mixed list, few vless: ≥50% proxy lines
	mixed := "vmess://abc\ntrojan://p@h:1\nss://x@y:2\n" + tlsWS + "\nhello\n"
	st, _ = Analyze([]byte(mixed))
	if !st.LooksLikeSubscription() {
		t.Fatalf("mixed should be accepted: %+v", st)
	}
	readme := "# My repo\nSome text\nmore text\nlink " + tlsWS + "\nand more\nand more\n"
	st, _ = Analyze([]byte(readme))
	if st.LooksLikeSubscription() {
		t.Fatalf("readme should be rejected: %+v", st)
	}
	// unacceptable configs don't count as valid
	st, _ = Analyze([]byte("vless://u@1.2.3.4:443?security=none\nvless://u@10.0.0.1:443?security=tls\n"))
	if st.Valid != 0 || st.VlessLinks != 2 {
		t.Fatalf("stats %+v", st)
	}
}
