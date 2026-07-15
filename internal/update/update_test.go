package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// recordingSink captures the last SetUpdate call for assertions.
type recordingSink struct {
	mu    sync.Mutex
	calls int
	last  updateArgs
}

type updateArgs struct {
	available     bool
	latestVersion string
	releaseURL    string
	inContainer   bool
}

func (r *recordingSink) SetUpdate(available bool, latestVersion, releaseURL string, inContainer bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.last = updateArgs{available, latestVersion, releaseURL, inContainer}
}

func (r *recordingSink) snapshot() (int, updateArgs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.last
}

// checkerFor builds a Checker pointed at ts, so check() exercises the real
// HTTP path against a canned response.
func checkerFor(current string, inContainer bool, sink Sink, url string) *Checker {
	c := New(current, inContainer, sink)
	c.url = url
	return c
}

func TestCheckReportsNewerRelease(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://github.com/vilaca/devpit/releases/tag/v0.2.0"}`))
	}))
	defer ts.Close()

	sink := &recordingSink{}
	checkerFor("v0.1.6", true, sink, ts.URL).check(context.Background())

	calls, got := sink.snapshot()
	if calls != 1 {
		t.Fatalf("SetUpdate calls = %d, want 1", calls)
	}
	want := updateArgs{true, "v0.2.0", "https://github.com/vilaca/devpit/releases/tag/v0.2.0", true}
	if got != want {
		t.Errorf("SetUpdate args = %+v, want %+v", got, want)
	}
}

func TestCheckNoReportWhenNotNewer(t *testing.T) {
	cases := map[string]string{
		"equal": "v0.1.6",
		"older": "v0.1.5",
	}
	for name, tag := range cases {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
			}))
			defer ts.Close()

			sink := &recordingSink{}
			checkerFor("v0.1.6", false, sink, ts.URL).check(context.Background())

			if calls, _ := sink.snapshot(); calls != 0 {
				t.Errorf("SetUpdate calls = %d, want 0 for tag %q", calls, tag)
			}
		})
	}
}

func TestCheck404IsQuiet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	sink := &recordingSink{}
	checkerFor("v0.1.6", false, sink, ts.URL).check(context.Background())

	if calls, _ := sink.snapshot(); calls != 0 {
		t.Errorf("SetUpdate calls = %d, want 0 on 404", calls)
	}
}

func TestCheckMalformedTagIsQuiet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"nightly-build"}`))
	}))
	defer ts.Close()

	sink := &recordingSink{}
	checkerFor("v0.1.6", false, sink, ts.URL).check(context.Background())

	if calls, _ := sink.snapshot(); calls != 0 {
		t.Errorf("SetUpdate calls = %d, want 0 on unparseable tag", calls)
	}
}

func TestStartSkipsDevBuild(t *testing.T) {
	// A dev build must never hit the network; point at a server that fails the
	// test if it is ever called, then confirm Start returns without checking.
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("dev build must not poll the releases API")
	}))
	defer ts.Close()

	sink := &recordingSink{}
	c := checkerFor("dev", false, sink, ts.URL)
	c.Start(context.Background()) // returns immediately, launches no goroutine

	if calls, _ := sink.snapshot(); calls != 0 {
		t.Errorf("SetUpdate calls = %d, want 0 for dev build", calls)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.6", "v0.2.0", true},
		{"v0.1.6", "v0.1.7", true},
		{"v0.1.6", "v1.0.0", true},
		{"v0.1.6", "v0.1.6", false},
		{"v0.1.6", "v0.1.5", false},
		{"v0.2.0", "v0.1.9", false},
		{"dev", "v0.2.0", false},        // dev never parses → no update
		{"v0.1.6", "garbage", false},    // malformed latest
		{"v0.1.6", "v0.1.6-rc1", false}, // pre-release patch does not parse
		{"v0.1.6", "v0.1", false},       // too few components
	}
	for _, tc := range cases {
		if got := newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}
