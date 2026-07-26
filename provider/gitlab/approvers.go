package gitlab

import (
	"context"
	"fmt"
	"time"
)

const approverTTL = 15 * time.Minute

type approverEntry struct {
	isSole    bool
	fetchedAt time.Time
}

type glMember struct {
	Username    string `json:"username"`
	AccessLevel int    `json:"access_level"`
}

type glProject struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
}

// isSoleApproverCached checks the in-memory approver cache and calls
// probeIsSoleApprover when the entry is missing or older than approverTTL.
func (p *Provider) isSoleApproverCached(ctx context.Context, projectPath string) (bool, error) {
	if e, ok := p.approverCache[projectPath]; ok && time.Since(e.fetchedAt) < approverTTL {
		return e.isSole, nil
	}
	isSole, err := p.probeIsSoleApprover(ctx, projectPath)
	if err != nil {
		return false, err
	}
	p.approverCache[projectPath] = approverEntry{isSole: isSole, fetchedAt: time.Now()}
	return isSole, nil
}

// probeIsSoleApprover calls GET /projects/:path/members/all?min_access_level=40
// and returns true when the authenticated user is the only account with
// access_level >= 40 (Maintainer or higher).
func (p *Provider) probeIsSoleApprover(ctx context.Context, projectPath string) (bool, error) {
	base := fmt.Sprintf("%s/projects/%s/members/all?min_access_level=40&per_page=100",
		p.apiBase, encodeProjectPath(projectPath))
	var all []glMember
	for u := base; u != ""; {
		resp, err := p.do(ctx, u)
		if err != nil {
			return false, err
		}
		next := resp.Header.Get("X-Next-Page")
		var page []glMember
		if err := decodeJSON(resp, &page); err != nil {
			return false, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		u = base + "&page=" + next
	}

	mergeCapable := 0
	foundMe := false
	for _, m := range all {
		if m.AccessLevel >= 40 {
			mergeCapable++
			if m.Username == p.handle {
				foundMe = true
			}
		}
	}
	return mergeCapable == 1 && foundMe, nil
}

// fetchOwnedProjects returns projects where the user has access_level >= 40.
func (p *Provider) fetchOwnedProjects(ctx context.Context) ([]glProject, error) {
	base := p.apiBase + "/projects?membership=true&min_access_level=40&archived=false&per_page=100"
	var all []glProject
	for u := base; u != ""; {
		resp, err := p.do(ctx, u)
		if err != nil {
			return nil, err
		}
		next := resp.Header.Get("X-Next-Page")
		var page []glProject
		if err := decodeJSON(resp, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		u = base + "&page=" + next
	}
	return all, nil
}

// encodeProjectPath percent-encodes a "group/project" path for use in GitLab
// REST URL path segments (replacing "/" with "%2F").
func encodeProjectPath(path string) string {
	encoded := make([]byte, 0, len(path))
	for i := range len(path) {
		if path[i] == '/' {
			encoded = append(encoded, '%', '2', 'F')
		} else {
			encoded = append(encoded, path[i])
		}
	}
	return string(encoded)
}
