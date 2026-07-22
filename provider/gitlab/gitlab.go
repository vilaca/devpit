package gitlab

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
	sdk.Registry["gitlab"] = func(cfg sdk.ConnectionConfig) (sdk.Provider, error) {
		return New(cfg)
	}
}

const userAgent = "devpit/0.1"

// Provider is the GitLab implementation of sdk.Provider.
type Provider struct {
	cfg             sdk.ConnectionConfig
	apiBase         string
	graphqlEndpoint string
	handle          string
	http            *http.Client
	// openSnapshots caches the last full item.observed payload for each open MR,
	// keyed by native ID. Populated by Reconcile; read by FastPoll's open-set
	// refresh. No lock needed — FastPoll and Reconcile are serialised per connection.
	openSnapshots map[string]sdk.ItemObservedPayload
}

// New builds a GitLab provider. BaseURL is the instance host (e.g.
// "https://gitlab.com"); the REST API lives at "{base}/api/v4".
func New(cfg sdk.ConnectionConfig) (*Provider, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	return &Provider{
		cfg:             cfg,
		apiBase:         base + "/api/v4",
		graphqlEndpoint: base + "/api/graphql",
		http:            &http.Client{Timeout: 30 * time.Second},
		openSnapshots:   map[string]sdk.ItemObservedPayload{},
	}, nil
}

// Capabilities implements sdk.Provider.
func (p *Provider) Capabilities() sdk.Capabilities {
	return sdk.Capabilities{
		FastSignal:       true,
		MergeGate:        true,
		ChangesRequested: true,
		ConditionalReqs:  false,
	}
}

// Close implements sdk.Provider.
func (p *Provider) Close(_ context.Context) error {
	p.http.CloseIdleConnections()
	return nil
}

func (p *Provider) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}

	switch {
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

// parseRateDelay extracts the retry delay from the Retry-After header (seconds).
func parseRateDelay(resp *http.Response) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

func decodeJSON(resp *http.Response, v any) error {
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: %w", sdk.ErrParse, err)
	}
	return nil
}

func rateRemaining(h http.Header) *int {
	s := h.Get("Ratelimit-Remaining")
	if s == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return nil
	}
	return &n
}
