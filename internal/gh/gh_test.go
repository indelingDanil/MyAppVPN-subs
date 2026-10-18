package gh

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestRateLimitWait(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	h := http.Header{}
	h.Set("Retry-After", "30")
	if d := rateLimitWait(h, now); d != 31*time.Second {
		t.Errorf("Retry-After: %s", d)
	}
	h = http.Header{}
	h.Set("X-RateLimit-Remaining", "0")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(40*time.Second).Unix(), 10))
	if d := rateLimitWait(h, now); d != 42*time.Second {
		t.Errorf("reset: %s", d)
	}
	if d := rateLimitWait(http.Header{}, now); d != time.Minute {
		t.Errorf("default: %s", d)
	}
}
