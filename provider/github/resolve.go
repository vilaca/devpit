package github

import (
	"context"
	"errors"
	"net/http"

	"github.com/vilaca/devpit/sdk"
)

// ResolveOpen implements sdk.Provider: it reports which native IDs are still
// open PRs. 404/410 are treated as gone (omitted); any other fetch error is
// returned so the engine does not reap mention leftovers this cycle.
func (p *Provider) ResolveOpen(ctx context.Context, nativeIDs []string) ([]string, error) {
	var open []string
	for _, nid := range nativeIDs {
		owner, repo, number, ok := parseGHNativeID(nid)
		if !ok {
			continue
		}
		pr, err := p.fetchPull(ctx, owner, repo, number)
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
		if observedIsOpen(p.observedFromPull(*pr)) {
			open = append(open, nid)
		}
	}
	return open, nil
}

func goneStatus(err error) bool {
	var se *sdk.StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status == http.StatusNotFound || se.Status == http.StatusGone
}
