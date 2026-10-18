// Package fetch downloads subscription bodies with a size cap, timeout and retries.
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserAgent identifies the bot to source hosts.
const UserAgent = "MyAppVPN-subs/1.0 (+https://github.com/indelingDanil/MyAppVPN-subs)"

// MaxBody is the default body cap (2 MiB).
const MaxBody = 2 << 20

// Client is a small HTTP getter.
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
	MaxBody int64
	Retries int
}

// Response is a fetched body plus the bits of metadata the pipeline uses.
type Response struct {
	Body         []byte
	Truncated    bool      // body exceeded MaxBody and was cut at the last newline
	Status       int       // HTTP status (0 on network error)
	LastModified time.Time // zero when the header is absent/unparseable
}

// Gone reports whether the status means "repo/file removed" (404, 410, 451).
func Gone(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone || status == http.StatusUnavailableForLegalReasons
}

// New returns a Client with the collect defaults (30 s, 2 MiB, 2 retries).
func New() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Timeout: 30 * time.Second,
		MaxBody: MaxBody,
		Retries: 2,
	}
}

// Get fetches url. Non-200 answers return an error AND a Response carrying the status.
// Bodies larger than MaxBody are truncated rather than rejected.
func (c *Client) Get(ctx context.Context, url string) (Response, error) {
	var r Response
	var err error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return r, ctx.Err()
			case <-time.After(time.Duration(attempt*2) * time.Second):
			}
		}
		var retry bool
		r, retry, err = c.once(ctx, url)
		if err == nil || !retry {
			return r, err
		}
	}
	return r, err
}

func (c *Client) once(ctx context.Context, url string) (Response, bool, error) {
	var r Response
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return r, false, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return r, true, err
	}
	defer resp.Body.Close()
	r.Status = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == 429 || resp.StatusCode >= 500
		return r, retry, fmt.Errorf("http %d", resp.StatusCode)
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			r.LastModified = t.UTC()
		}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxBody+1))
	if err != nil {
		return r, true, err
	}
	if int64(len(b)) > c.MaxBody {
		b = b[:c.MaxBody]
		if i := bytes.LastIndexByte(b, '\n'); i > 0 {
			b = b[:i+1]
		}
		r.Truncated = true
	}
	r.Body = b
	return r, false, nil
}
