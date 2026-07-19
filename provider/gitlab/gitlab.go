package gitlab

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
	sdk.Registry["gitlab"] = func(cfg sdk.ConnectionConfig) (sdk.Provider, error) {
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

func New(cfg sdk.ConnectionConfig) (*Provider, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	return &Provider{
		cfg:     cfg,
		apiBase: base + "/api/v4",
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *Provider) Capabilities() sdk.Capabilities {
	return sdk.Capabilities{
		FastSignal:       true,
		MergeGate:        true,
		ChangesRequested: true,
		ConditionalReqs:  false,
	}
}

func (p *Provider) Close(ctx context.Context) error {
	p.http.CloseIdleConnections()
	return nil
}

type rateError struct {
	retryAfter string
}

func (e *rateError) Error() string { return "provider: rate limited" }
func (e *rateError) Unwrap() error { return sdk.ErrRateLimited }

func (p *Provider) do(ctx context.Context, method, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
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
		resp.Body.Close()
		return nil, sdk.ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		ra := resp.Header.Get("Retry-After")
		resp.Body.Close()
		return nil, &rateError{retryAfter: ra}
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("gitlab: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func rateRemaining(h http.Header) *int {
	s := h.Get("RateLimit-Remaining")
	if s == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return nil
	}
	return &n
}
