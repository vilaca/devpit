package gitlab

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vilaca/devpit/sdk"
)

const resolveOpenMR = `{"iid":7,"project_id":1,"title":"T",` +
	`"web_url":"https://gitlab.com/acme/api/-/merge_requests/7",` +
	`"state":"opened","author":{"username":"alice"},` +
	`"references":{"full":"acme/api!7"},"updated_at":"2026-07-10T00:00:00Z"}`

// seqRT returns a scripted sequence of responses, one per call, so a multi-fetch
// ResolveOpen can be driven through a mid-loop error.
type seqRT struct {
	resps []stubRT
	i     int
}

func (rt *seqRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	r := rt.resps[min(rt.i, len(rt.resps)-1)]
	rt.i++
	h := r.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

// TestResolveOpenPartialOnError verifies that when a later fetch errors, the IDs
// already confirmed open are returned alongside the error rather than dropped.
// The engine ignores the slice on error (fail-closed, internal/engine
// confirmGone), so this is an honesty fix, not a behaviour change for the caller.
func TestResolveOpenPartialOnError(t *testing.T) {
	p, err := New(sdk.ConnectionConfig{Type: "gitlab", Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.http.Transport = &seqRT{resps: []stubRT{
		{status: http.StatusOK, body: resolveOpenMR},       // acme/api!7 → open
		{status: http.StatusInternalServerError, body: ""}, // acme/api!8 → error
	}}

	got, err := p.ResolveOpen(context.Background(), []string{"acme/api!7", "acme/api!8"})
	if !errors.Is(err, sdk.ErrServer) {
		t.Fatalf("err = %v, want ErrServer", err)
	}
	if !reflect.DeepEqual(got, []string{"acme/api!7"}) {
		t.Errorf("open = %v, want [acme/api!7] (confirmed IDs kept despite the error)", got)
	}
}

func TestResolveOpen(t *testing.T) {
	cases := []struct {
		name    string
		ids     []string
		status  int
		body    string
		want    []string
		wantErr error
	}{
		{"empty input", nil, 0, "", nil, nil},
		{"open", []string{"acme/api!7"}, http.StatusOK, resolveOpenMR, []string{"acme/api!7"}, nil},
		{"merged", []string{"acme/api!7"}, http.StatusOK,
			`{"iid":7,"state":"merged","author":{"username":"alice"},"references":{"full":"acme/api!7"}}`,
			nil, nil},
		{"closed", []string{"acme/api!7"}, http.StatusOK,
			`{"iid":7,"state":"closed","author":{"username":"alice"},"references":{"full":"acme/api!7"}}`,
			nil, nil},
		{"not found is gone", []string{"acme/api!7"}, http.StatusNotFound, "", nil, nil},
		{"gone is gone", []string{"acme/api!7"}, http.StatusGone, "", nil, nil},
		{"unparseable id is omitted", []string{"not-an-id"}, 0, "", nil, nil},
		{"server error fails closed", []string{"acme/api!7"}, http.StatusInternalServerError, "", nil, sdk.ErrServer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newStubProvider(t, stubRT{status: c.status, body: c.body})
			got, err := p.ResolveOpen(context.Background(), c.ids)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveOpen: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("open = %v, want %v", got, c.want)
			}
		})
	}
}
