package pipeline

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/indelingDanil/MyAppVPN-subs/internal/vless"
)

// Check verifies a published tree against the app contract: every .txt line parses and
// passes the app filter, remarks start with a flag (or 🌐), ≤500 lines and <1 MB per file,
// .b64 files decode to their .txt twins, and main files contain no UA/RU exits.
func Check(out string, maxLines int) error {
	var problems []string
	files, _ := filepath.Glob(filepath.Join(out, "*.txt"))
	cc, _ := filepath.Glob(filepath.Join(out, "by-country", "*.txt"))
	files = append(files, cc...)
	if len(files) == 0 {
		return fmt.Errorf("no .txt files in %s", out)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if len(b) >= MaxFileBytes {
			problems = append(problems, fmt.Sprintf("%s: %d bytes", f, len(b)))
		}
		ls := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(ls) > maxLines {
			problems = append(problems, fmt.Sprintf("%s: %d lines", f, len(ls)))
		}
		isMain := filepath.Dir(f) == filepath.Clean(out)
		for i, l := range ls {
			if l == "" {
				continue
			}
			c, err := vless.Parse(l)
			if err != nil || !c.AppAccepts() {
				problems = append(problems, fmt.Sprintf("%s:%d rejected by app filter", f, i+1))
				continue
			}
			fields := strings.Fields(c.Remark)
			if len(fields) != 3 || !strings.HasPrefix(fields[2], c.Security+"-") {
				problems = append(problems, fmt.Sprintf("%s:%d bad remark %q", f, i+1, c.Remark))
				continue
			}
			if isMain && (fields[1] == "UA" || fields[1] == "RU") {
				problems = append(problems, fmt.Sprintf("%s:%d %s exit in main file", f, i+1, fields[1]))
			}
		}
		if isMain {
			b64 := strings.TrimSuffix(f, ".txt") + ".b64"
			if eb, err := os.ReadFile(b64); err == nil {
				dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(eb)))
				if err != nil || !bytes.HasPrefix(b, dec) {
					problems = append(problems, fmt.Sprintf("%s does not decode to a prefix of %s", b64, f))
				}
				if len(eb) >= MaxFileBytes {
					problems = append(problems, fmt.Sprintf("%s: %d bytes", b64, len(eb)))
				}
			}
		}
	}
	if len(problems) > 0 {
		if len(problems) > 20 {
			problems = append(problems[:20], fmt.Sprintf("… and %d more", len(problems)-20))
		}
		return fmt.Errorf("check failed:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
