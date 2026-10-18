// Package geo maps IPs to ISO country codes (GeoLite2 / DB-IP lite mmdb) and builds the
// remarks the MyAppVPN client parses (`<flag> <CC> <security>-<NNNN>`).
package geo

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Unknown is used when the country can't be determined.
const Unknown = "XX"

// DB wraps an mmdb country database. A nil *DB answers Unknown for everything.
type DB struct{ r *maxminddb.Reader }

// Open opens an mmdb file. An empty path returns a nil DB (everything unknown).
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, nil
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &DB{r: r}, nil
}

// Close releases the database.
func (d *DB) Close() {
	if d != nil && d.r != nil {
		_ = d.r.Close()
	}
}

// record covers both schemas: MaxMind/DB-IP "country.iso_code" and the flat
// "country_code" used by some re-packagings (e.g. ip-location-db).
type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	RegisteredCountry struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"registered_country"`
	CountryCode string `maxminddb:"country_code"`
}

// Country returns the upper-case ISO 3166-1 alpha-2 code for ip, or Unknown.
func (d *DB) Country(ip string) string {
	if d == nil || d.r == nil {
		return Unknown
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return Unknown
	}
	var rec record
	if err := d.r.Lookup(a.Unmap()).Decode(&rec); err != nil {
		return Unknown
	}
	for _, cc := range []string{rec.Country.ISOCode, rec.CountryCode, rec.RegisteredCountry.ISOCode} {
		if NormalizeCC(cc) != Unknown {
			return NormalizeCC(cc)
		}
	}
	return Unknown
}

// NormalizeCC upper-cases a 2-letter code; anything else becomes Unknown.
func NormalizeCC(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return Unknown
	}
	return cc
}

// Flag returns the regional-indicator flag emoji for a 2-letter code, or 🌐 for Unknown.
func Flag(cc string) string {
	cc = NormalizeCC(cc)
	if cc == Unknown {
		return "🌐"
	}
	return string([]rune{0x1F1E6 + rune(cc[0]-'A'), 0x1F1E6 + rune(cc[1]-'A')})
}

// Remark builds `<flag> <CC> <security>-<NNNN>`, e.g. "🇩🇪 DE reality-0012".
func Remark(cc, security string, n int) string {
	cc = NormalizeCC(cc)
	return fmt.Sprintf("%s %s %s-%04d", Flag(cc), cc, security, n)
}
