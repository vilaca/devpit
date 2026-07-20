package gitlab

import (
	"context"

	"github.com/vilaca/devpit/sdk"
)

// ResolveIdentity implements sdk.Provider: it fetches the authenticated user
// and returns their canonical handle.
func (p *Provider) ResolveIdentity(ctx context.Context) (sdk.Identity, error) {
	resp, err := p.do(ctx, p.apiBase+"/user")
	if err != nil {
		return sdk.Identity{}, err
	}
	var u glUser
	if err := decodeJSON(resp, &u); err != nil {
		return sdk.Identity{}, err
	}
	if u.Username == "" {
		if p.cfg.Handle != "" {
			p.handle = p.cfg.Handle
			return sdk.Identity{Handle: p.cfg.Handle}, nil
		}
		return sdk.Identity{}, sdk.ErrManualIdentityRequired
	}
	p.handle = u.Username
	return sdk.Identity{Handle: u.Username, DisplayName: u.Name}, nil
}
