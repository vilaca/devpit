package github

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

type ghCollaborator struct {
	Login       string `json:"login"`
	Permissions struct {
		Push     bool `json:"push"`
		Maintain bool `json:"maintain"`
		Admin    bool `json:"admin"`
	} `json:"permissions"`
}

// keepAsSoleApprover returns true when it should be included under the
// sole_approver role: not a draft, not self-authored, and sole approver on the repo.
func (p *Provider) keepAsSoleApprover(ctx context.Context, it ghSearchItem, repo string) (bool, error) {
	if it.Draft || it.User.Login == p.handle {
		return false, nil
	}
	return p.isSoleApproverCached(ctx, repo)
}

// isSoleApproverCached checks the in-memory approver cache and calls
// probeIsSoleApprover when the entry is missing or older than approverTTL.
// fullRepo is "owner/repo" (e.g. "octocat/myrepo").
func (p *Provider) isSoleApproverCached(ctx context.Context, fullRepo string) (bool, error) {
	if e, ok := p.approverCache[fullRepo]; ok && time.Since(e.fetchedAt) < approverTTL {
		return e.isSole, nil
	}
	isSole, err := p.probeIsSoleApprover(ctx, fullRepo)
	if err != nil {
		return false, err
	}
	p.approverCache[fullRepo] = approverEntry{isSole: isSole, fetchedAt: time.Now()}
	return isSole, nil
}

// probeIsSoleApprover calls GET /repos/{owner}/{repo}/collaborators and returns
// true when the authenticated user is the only account with push/maintain/admin
// permission. fullRepo is "owner/repo".
func (p *Provider) probeIsSoleApprover(ctx context.Context, fullRepo string) (bool, error) {
	u := fmt.Sprintf("%s/repos/%s/collaborators?affiliation=direct&per_page=100", p.apiBase, fullRepo)
	var all []ghCollaborator
	for u != "" {
		resp, err := p.do(ctx, u, nil)
		if err != nil {
			return false, err
		}
		next := nextLink(resp.Header)
		var page []ghCollaborator
		if err := decodeJSON(resp, &page); err != nil {
			return false, err
		}
		all = append(all, page...)
		u = next
	}

	mergeCapable := 0
	foundMe := false
	for _, c := range all {
		if c.Permissions.Push || c.Permissions.Maintain || c.Permissions.Admin {
			mergeCapable++
			if c.Login == p.handle {
				foundMe = true
			}
		}
	}
	return mergeCapable == 1 && foundMe, nil
}
