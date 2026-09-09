package dtrack

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Probe discovers the server version and whether the v2 REST API is available
// for the resources the exporter needs. It is best-effort: failures leave the
// client in v1-only mode. Safe to call once at startup.
func (c *Client) Probe(ctx context.Context) {
	var v struct {
		Version string `json:"version"`
	}
	if _, err := c.do(ctx, "/api/version", nil, &v); err == nil {
		c.serverVersion = v.Version
	}

	if c.apiVersion == APIv1 {
		return
	}

	// Cheap capability probes: ask for a single item from each v2 collection.
	probe := func(path string) bool {
		q := url.Values{}
		q.Set("limit", "1")
		_, err := c.do(ctx, path, q, &struct{}{})
		return err == nil
	}
	if c.apiVersion == APIv2 || c.apiVersion == APIAuto {
		c.v2Projects = probe("/api/v2/projects")
		c.v2Violations = probe("/api/v2/policy-violations")
	}
}

// maxPages bounds every paginated walk so a misbehaving server (a stuck
// next_page_token, a pageNumber that is ignored) can never loop forever.
const maxPages = 100_000

func cloneValues(base url.Values) url.Values {
	q := make(url.Values, len(base))
	for k, vs := range base {
		q[k] = append([]string(nil), vs...)
	}
	return q
}

// tokenPage is the envelope returned by v2 token-paginated collections.
type tokenPage[T any] struct {
	Items         []T    `json:"items"`
	NextPageToken string `json:"next_page_token"`
}

// paginateToken walks a v2 token-paginated collection to completion.
func paginateToken[T any](ctx context.Context, c *Client, path string, base url.Values) ([]T, error) {
	var out []T
	token := ""
	for page := 0; page < maxPages; page++ {
		q := cloneValues(base)
		q.Set("limit", strconv.Itoa(c.pageSize))
		if token != "" {
			q.Set("pageToken", token)
		}
		var body tokenPage[T]
		if _, err := c.do(ctx, path, q, &body); err != nil {
			return nil, err
		}
		out = append(out, body.Items...)
		// Stop on an empty page, a missing token, or a token that did not
		// advance (server bug) - any of which means there is nothing more.
		if len(body.Items) == 0 || body.NextPageToken == "" || body.NextPageToken == token {
			return out, nil
		}
		token = body.NextPageToken
	}
	return out, fmt.Errorf("dtrack: GET %s: pagination exceeded %d pages", path, maxPages)
}

// paginateOffset walks a v1 pageNumber/pageSize collection to completion. The
// X-Total-Count header is the authority on when to stop; a short page is only
// treated as the end when the server does not send that header.
func paginateOffset[T any](ctx context.Context, c *Client, path string, base url.Values) ([]T, error) {
	var out []T
	for pageNumber := 1; pageNumber <= maxPages; pageNumber++ {
		q := cloneValues(base)
		q.Set("pageNumber", strconv.Itoa(pageNumber))
		q.Set("pageSize", strconv.Itoa(c.pageSize))

		var items []T
		hdr, err := c.do(ctx, path, q, &items)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)

		total := totalCount(hdr)
		switch {
		case len(items) == 0:
			return out, nil
		case total > 0:
			if len(out) >= total {
				return out, nil
			}
		case len(items) < c.pageSize:
			// No X-Total-Count and a short page: assume this was the last one.
			return out, nil
		}
	}
	return out, fmt.Errorf("dtrack: GET %s: pagination exceeded %d pages", path, maxPages)
}

// PortfolioMetrics returns the latest portfolio-wide metrics. Only the v1
// endpoint exists for this resource.
func (c *Client) PortfolioMetrics(ctx context.Context) (Metrics, error) {
	var m Metrics
	_, err := c.do(ctx, "/api/v1/metrics/portfolio/current", nil, &m)
	return m, err
}

// Projects returns every project with its embedded latest metrics.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	if c.v2Projects {
		return paginateToken[Project](ctx, c, "/api/v2/projects", nil)
	}
	return paginateOffset[Project](ctx, c, "/api/v1/project", nil)
}

// PolicyViolations returns every policy violation, including suppressed ones.
func (c *Client) PolicyViolations(ctx context.Context) ([]PolicyViolation, error) {
	if c.v2Violations {
		q := url.Values{}
		q.Set("suppressed", "true")
		return paginateToken[PolicyViolation](ctx, c, "/api/v2/policy-violations", q)
	}
	q := url.Values{}
	q.Set("suppressed", "true")
	return paginateOffset[PolicyViolation](ctx, c, "/api/v1/violation", q)
}
