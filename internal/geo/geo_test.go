package geo

import "testing"

func TestFlagAndRemark(t *testing.T) {
	if got := Flag("de"); got != "🇩🇪" {
		t.Errorf("Flag(de)=%q", got)
	}
	if got := Flag("XX"); got != "🌐" {
		t.Errorf("Flag(XX)=%q", got)
	}
	if got := Flag("???"); got != "🌐" {
		t.Errorf("Flag(???)=%q", got)
	}
	if got := Remark("DE", "reality", 12); got != "🇩🇪 DE reality-0012" {
		t.Errorf("Remark=%q", got)
	}
	if got := Remark("", "tls", 7); got != "🌐 XX tls-0007" {
		t.Errorf("Remark unknown=%q", got)
	}
}

func TestNilDB(t *testing.T) {
	var d *DB
	if d.Country("1.1.1.1") != Unknown {
		t.Error("nil DB must answer Unknown")
	}
	d2, err := Open("")
	if err != nil || d2 != nil {
		t.Fatal("Open(\"\") should give nil, nil")
	}
}
