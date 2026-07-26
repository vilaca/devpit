package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

// Provider is the GitHub implementation of sdk.Provider.
type Provider struct {
	cfg             sdk.ConnectionConfig
	apiBase         string
	graphqlEndpoint string
	handle          string
	http            *http.Client
	approverCache   map[string]approverEntry // key: "owner/repo"; no lock needed (serialised per connection)
}

// New builds a GitHub provider. BaseURL is the web host (e.g.
// "https://github.com"); the REST API lives at api.github.com for the public
// host and at "{base}/api/v3" for Enterprise.
func New(cfg sdk.ConnectionConfig) (*Provider, error) {
	base := apiBase(cfg.BaseURL)
	return &Provider{
		cfg:             cfg,
		apiBase:         base,
		graphqlEndpoint: strings.TrimSuffix(base, "/v3") + "/graphql",
		http:            &http.Client{Timeout: 30 * time.Second},
		approverCache:   make(map[string]approverEntry),
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

// Capabilities implements sdk.Provider.
func (p *Provider) Capabilities() sdk.Capabilities {
	return sdk.Capabilities{
		FastSignal:       true,
		MergeGate:        true,
		ChangesRequested: true,
		ConditionalReqs:  true,
	}
}

// Close implements sdk.Provider.
func (p *Provider) Close(_ context.Context) error {
	p.http.CloseIdleConnections()
	return nil
}

// do issues a request with auth + UA headers and classifies the HTTP status
// into sentinel errors. On 2xx it returns the response for the caller to
// decode; callers must close the body. On 304 it returns the response too
// (status is preserved for the conditional-request path).
func (p *Provider) do(ctx context.Context, url string, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		_ = resp.Body.Close()
		return nil, sdk.ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		d := parseRateDelay(resp)
		_ = resp.Body.Close()
		return nil, &sdk.RateLimitError{RetryAfter: d}
	default:
		_ = resp.Body.Close()
		return nil, &sdk.StatusError{Status: resp.StatusCode}
	}
}

// parseRateDelay extracts the retry delay from Retry-After (seconds) and
// X-RateLimit-Reset (Unix timestamp), returning the larger of the two.
func parseRateDelay(resp *http.Response) time.Duration {
	var d time.Duration
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			d = time.Duration(secs) * time.Second
		}
	}
	if reset := resp.Header.Get("X-Ratelimit-Reset"); reset != "" {
		if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
			if until := time.Until(time.Unix(ts, 0)); until > d {
				d = until
			}
		}
	}
	return d
}

func decodeJSON(resp *http.Response, v any) error {
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: %w", sdk.ErrParse, err)
	}
	return nil
}

// rateRemaining reads X-RateLimit-Remaining for the sync log; nil if absent.
func rateRemaining(h http.Header) *int {
	s := h.Get("X-Ratelimit-Remaining")
	if s == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return nil
	}
	return &n
}
