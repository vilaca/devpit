package github

import (
	"context"

	"github.com/vilaca/devpit/sdk"
)

// ResolveIdentity implements sdk.Provider: it fetches the authenticated user
// and returns their canonical handle.
func (p *Provider) ResolveIdentity(ctx context.Context) (sdk.Identity, error) {
	resp, err := p.do(ctx, p.apiBase+"/user", nil)
	if err != nil {
		return sdk.Identity{}, err
	}
	var u ghUser
	if err := decodeJSON(resp, &u); err != nil {
		return sdk.Identity{}, err
	}
	if u.Login == "" {
		if p.cfg.Handle != "" {
			p.handle = p.cfg.Handle
			return sdk.Identity{Handle: p.cfg.Handle}, nil
		}
		return sdk.Identity{}, sdk.ErrManualIdentityRequired
	}
	p.handle = u.Login
	return sdk.Identity{Handle: u.Login, DisplayName: u.Name}, nil
}
