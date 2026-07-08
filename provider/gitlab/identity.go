package gitlab

import (
	"context"

	"github.com/vilaca/devpit/sdk"
)

func (p *Provider) ResolveIdentity(ctx context.Context) (sdk.Identity, error) {
	resp, err := p.do(ctx, "GET", p.apiBase+"/user")
	if err != nil {
		return sdk.Identity{}, err
	}
	var u glUser
	if err := decodeJSON(resp, &u); err != nil {
		return sdk.Identity{}, err
	}
	if u.Username == "" {
		return sdk.Identity{}, sdk.ErrManualIdentityRequired
	}
	p.handle = u.Username
	return sdk.Identity{Handle: u.Username, DisplayName: u.Name}, nil
}
