package dtrack

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Secret wraps a sensitive string (the API key) so it can never be printed by
// accident. Its String/Format/Marshal representations are always redacted; the
// real value is only reachable via Reveal.
type Secret string

func (s Secret) String() string                { return "***" }
func (s Secret) GoString() string              { return `"***"` }
func (s Secret) Format(f fmt.State, verb rune) { _, _ = io.WriteString(f, "***") }
func (s Secret) MarshalText() ([]byte, error)  { return []byte("***"), nil }
func (s Secret) MarshalJSON() ([]byte, error)  { return []byte(`"***"`), nil }

// Reveal returns the underlying secret value. Only call this at the point the
// value is handed to the transport.
func (s Secret) Reveal() string { return string(s) }

// APIVersion selects which REST API the client prefers.
type APIVersion string

const (
	APIv1   APIVersion = "v1"
	APIv2   APIVersion = "v2"
	APIAuto APIVersion = "auto"
)

// Options configures a Client.
type Options struct {
	BaseURL            string
	APIKey             Secret
	APIVersion         APIVersion
	UserAgent          string
	RequestTimeout     time.Duration
	RetryMax           int
	RetryBaseDelay     time.Duration
	PageSize           int
	InsecureSkipVerify bool
}

const (
	defaultPageSize = 100
	maxPageSize     = 500
	minPageSize     = 1
)

// Client is a thin Dependency-Track API client.
type Client struct {
	base       *url.URL
	apiKey     Secret
	apiVersion APIVersion
	userAgent  string
	pageSize   int
	retryMax   int
	retryBase  time.Duration
	http       *http.Client

	// resolved by Probe / lazily; guards which API to use per resource.
	serverVersion string
	v2Projects    bool
	v2Violations  bool
}

// New builds a Client from Options, applying defaults and clamping.
func New(o Options) (*Client, error) {
	if o.BaseURL == "" {
		return nil, fmt.Errorf("dtrack: no base URL provided")
	}
	u, err := url.ParseRequestURI(strings.TrimRight(o.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("dtrack: invalid base URL: %w", err)
	}
	if o.APIKey == "" {
		return nil, fmt.Errorf("dtrack: no API key provided")
	}

	ps := o.PageSize
	if ps == 0 {
		ps = defaultPageSize
	}
	if ps < minPageSize {
		ps = minPageSize
	}
	if ps > maxPageSize {
		ps = maxPageSize
	}

	timeout := o.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	retryBase := o.RetryBaseDelay
	if retryBase <= 0 {
		retryBase = 500 * time.Millisecond
	}
	ua := o.UserAgent
	if ua == "" {
		ua = "dependency-track-exporter"
	}
	av := o.APIVersion
	if av == "" {
		av = APIAuto
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if o.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in
	}

	return &Client{
		base:       u,
		apiKey:     o.APIKey,
		apiVersion: av,
		userAgent:  ua,
		pageSize:   ps,
		retryMax:   max(0, o.RetryMax),
		retryBase:  retryBase,
		http:       &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// PageSize returns the resolved page size.
func (c *Client) PageSize() int { return c.pageSize }

// ServerVersion returns the Dependency-Track version discovered by Probe.
func (c *Client) ServerVersion() string { return c.serverVersion }

// do issues a GET request against path (relative to the API base), decoding a
// JSON body into out (which may be nil). It retries idempotent failures
// (5xx, 429, connection errors, timeouts) with exponential backoff.
func (c *Client) do(ctx context.Context, path string, query url.Values, out any) (http.Header, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryMax; attempt++ {
		if attempt > 0 {
			delay := c.retryBase << (attempt - 1)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("X-Api-Key", c.apiKey.Reveal())

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = redactErr(err, c.apiKey)
			continue // network error: retry
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			lastErr = fmt.Errorf("dtrack: GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return nil, fmt.Errorf("dtrack: GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return nil, fmt.Errorf("dtrack: GET %s: decode: %w", path, err)
			}
		}
		return resp.Header, nil
	}
	return nil, fmt.Errorf("dtrack: request failed after %d attempt(s): %w", c.retryMax+1, lastErr)
}

func redactErr(err error, s Secret) error {
	if s == "" {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), s.Reveal(), "***"))
}

func totalCount(h http.Header) int {
	n, _ := strconv.Atoi(h.Get("X-Total-Count"))
	return n
}
