// Package update is DevPit's self-update awareness. It polls the GitHub releases
// API for the latest published release and, when that release is newer than the
// running binary, reports it to a Sink so the UI can show a quiet "update
// available" hint. It is deliberately passive: it never downloads or installs
// anything (ADR-0023), and a binary built without a stamped version (version
// "dev") skips the check entirely. All failures are quiet — logged at the
// ambient noise level, never surfaced as errors — so a flaky network or an
// unreleased repo (404) simply means "no update".
package update

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// checkEvery is how often the checker re-polls after the startup check.
const checkEvery = 24 * time.Hour

// releasesURL is the unauthenticated GitHub API endpoint for DevPit's latest
// published release.
const releasesURL = "https://api.github.com/repos/vilaca/devpit/releases/latest"

// httpTimeout bounds a single releases call so a hung connection cannot wedge
// the checker goroutine.
const httpTimeout = 10 * time.Second

// Sink receives the result of each successful check. *api.Server implements it
// (it stores the status for GET /connections and nudges the SSE stream); the
// interface is declared here so this package needs no dependency on internal/api.
type Sink interface {
	SetUpdate(available bool, latestVersion, releaseURL string, inContainer bool)
}

// Checker polls the releases API on a cadence and reports newer releases to a
// Sink. Construct with New.
type Checker struct {
	current     string
	inContainer bool
	url         string
	client      *http.Client
	sink        Sink
}

// New constructs a Checker for the running version. inContainer selects the
// upgrade hint the UI shows (docker vs. brew) and is forwarded to the Sink. The
// check is skipped entirely when current is the unstamped dev version.
func New(current string, inContainer bool, sink Sink) *Checker {
	return &Checker{
		current:     current,
		inContainer: inContainer,
		url:         releasesURL,
		client:      &http.Client{Timeout: httpTimeout},
		sink:        sink,
	}
}

// Start runs an immediate check, then re-checks every checkEvery, in a new
// goroutine that returns until ctx is cancelled. A dev build never checks.
func (c *Checker) Start(ctx context.Context) {
	if c.current == "dev" {
		return
	}
	go c.loop(ctx)
}

func (c *Checker) loop(ctx context.Context) {
	c.check(ctx)
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.check(ctx)
		}
	}
}

// release is the subset of the releases/latest payload we read.
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// check performs one poll. A 404 (no releases published yet) means "no update"
// and is silent; every other failure logs quietly and leaves the last reported
// status untouched. The Sink is only nudged when a strictly newer release exists.
func (c *Checker) check(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		log.Printf("devpit: update check: build request: %v", err)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("devpit: update check: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return // no releases yet — quietly means "no update"
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("devpit: update check: unexpected status %d", resp.StatusCode)
		return
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		log.Printf("devpit: update check: decode: %v", err)
		return
	}
	if newer(c.current, rel.TagName) {
		c.sink.SetUpdate(true, rel.TagName, rel.HTMLURL, c.inContainer)
	}
}

// newer reports whether latest is a strictly higher version than current. Both
// are expected as "vMAJOR.MINOR.PATCH"; anything that does not parse cleanly
// yields false, so a malformed or pre-release tag is treated as "no update".
func newer(current, latest string) bool {
	cur, ok := parse(current)
	if !ok {
		return false
	}
	lat, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parse splits a "vMAJOR.MINOR.PATCH" tag into its three numeric components.
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
