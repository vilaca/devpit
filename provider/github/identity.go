package github

import (
	"context"

	"github.com/vilaca/devpit/sdk"
)

func (p *Provider) ResolveIdentity(ctx context.Context) (sdk.Identity, error) {
	resp, err := p.do(ctx, "GET", p.apiBase+"/user", nil)
	if err != nil {
		return sdk.Identity{}, err
	}
	var u ghUser
	if err := decodeJSON(resp, &u); err != nil {
		return sdk.Identity{}, err
	}
	if u.Login == "" {
		return sdk.Identity{}, sdk.ErrManualIdentityRequired
	}
	p.handle = u.Login
	return sdk.Identity{Handle: u.Login, DisplayName: u.Name}, nil
}
