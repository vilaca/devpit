package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vilaca/devpit/sdk"
)

func init() {
	sdk.Registry["github"] = func(cfg sdk.ConnectionConfig) (sdk.Provider, error) {
		return New(cfg)
	}
}

const userAgent = "devpit/0.1"

type Provider struct {
	cfg     sdk.ConnectionConfig
	apiBase string
	handle  string
	http    *http.Client
}

// New builds a GitHub provider. BaseURL is the web host (e.g.
// "https://github.com"); the REST API lives at api.github.com for the public
// host and at "{base}/api/v3" for Enterprise.
func New(cfg sdk.ConnectionConfig) (*Provider, error) {
	return &Provider{
		cfg:     cfg,
		apiBase: apiBase(cfg.BaseURL),
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func apiBase(baseURL string) string {
	b := strings.TrimRight(baseURL, "/")
	switch b {
	case "", "https://github.com", "http://github.com":
		return "https://api.github.com"
	default:
		return b + "/api/v3"
	}
}

func (p *Provider) Capabilities() sdk.Capabilities {
	return sdk.Capabilities{
		FastSignal:       true,
		MergeGate:        true,
		ChangesRequested: true,
		ConditionalReqs:  true,
	}
}

func (p *Provider) Close(ctx context.Context) error {
	p.http.CloseIdleConnections()
	return nil
}

// rateError carries a Retry-After hint so the caller can persist it as a
// cursor; the engine honors sync_cursors["gh.fast.retry_after"].
type rateError struct {
	retryAfter string
}

func (e *rateError) Error() string { return "provider: rate limited" }
func (e *rateError) Unwrap() error { return sdk.ErrRateLimited }

// do issues a request with auth + UA headers and classifies the HTTP status
// into sentinel errors. On 2xx it returns the response for the caller to
// decode; callers must close the body. On 304 it returns the response too
// (status is preserved for the conditional-request path).
func (p *Provider) do(ctx context.Context, method, url string, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+p.cfg.Token)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return resp, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return resp, nil
	case resp.StatusCode == http.StatusUnauthorized:
		resp.Body.Close()
		return nil, sdk.ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		ra := resp.Header.Get("Retry-After")
		resp.Body.Close()
		return nil, &rateError{retryAfter: ra}
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// rateRemaining reads X-RateLimit-Remaining for the sync log; nil if absent.
func rateRemaining(h http.Header) *int {
	s := h.Get("X-RateLimit-Remaining")
	if s == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return nil
	}
	return &n
}
