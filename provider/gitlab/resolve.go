package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/vilaca/devpit/sdk"
)

// ResolveOpen implements sdk.Provider: it reports which native IDs are still
// open MRs. 404/410 are treated as gone (omitted); any other fetch error is
// returned so the engine does not reap mention leftovers this cycle.
func (p *Provider) ResolveOpen(ctx context.Context, nativeIDs []string) ([]string, error) {
	var open []string
	for _, nid := range nativeIDs {
		fullPath, iid, ok := parseGLNativeID(nid)
		if !ok {
			continue
		}
		mr, err := p.fetchMRByPath(ctx, fullPath, iid)
		if err != nil {
			if goneStatus(err) {
				continue
			}
			// Return the IDs confirmed open so far alongside the error. The
			// engine ignores the slice unless err == nil (fail-closed reaping,
			// internal/engine confirmGone), so this loses no data — it just
			// avoids the misleading nil return.
			return open, err
		}
		pl, ok := p.observedFromMR(*mr).Payload.(sdk.ItemObservedPayload)
		if ok && pl.State == stateOpen {
			open = append(open, nid)
		}
	}
	return open, nil
}

func (p *Provider) fetchMRByPath(ctx context.Context, fullPath string, iid int) (*glMergeRequest, error) {
	u := fmt.Sprintf("%s/projects/%s/merge_requests/%d", p.apiBase, url.PathEscape(fullPath), iid)
	resp, err := p.do(ctx, u)
	if err != nil {
		return nil, err
	}
	var mr glMergeRequest
	if err := decodeJSON(resp, &mr); err != nil {
		return nil, err
	}
	return &mr, nil
}

func goneStatus(err error) bool {
	var se *sdk.StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status == http.StatusNotFound || se.Status == http.StatusGone
}
