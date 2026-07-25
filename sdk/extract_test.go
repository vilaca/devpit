package sdk

import (
	"reflect"
	"testing"
)

func TestExtractTicketKeys(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		want    []string
	}{
		{
			name:    "empty sources",
			sources: nil,
			want:    nil,
		},
		{
			name:    "no match in any source",
			sources: []string{"no ticket here", "also nothing"},
			want:    nil,
		},
		{
			name:    "match in title wins",
			sources: []string{"RPC-74504: Add feature", "rpc-feature", "RPC-99999 in description"},
			want:    []string{"RPC-74504"},
		},
		{
			name:    "skip empty first source, match in second",
			sources: []string{"no keys here", "feature/RPC-100-my-branch"},
			want:    []string{"RPC-100"},
		},
		{
			name:    "multiple keys in winning source, deduplicated",
			sources: []string{"RPC-1 and RPC-2 and RPC-1 again"},
			want:    []string{"RPC-1", "RPC-2"},
		},
		{
			name:    "placeholder RPC-XXXXX must not match",
			sources: []string{"RPC-XXXXX is not a ticket"},
			want:    nil,
		},
		{
			name:    "two-letter project key AB-1",
			sources: []string{"AB-1: fix"},
			want:    []string{"AB-1"},
		},
		{
			name:    "key with digits in project part AB2-123",
			sources: []string{"AB2-123"},
			want:    []string{"AB2-123"},
		},
		{
			name:    "lowercase key does not match",
			sources: []string{"rpc-123"},
			want:    nil,
		},
		{
			name:    "word boundary: XRPC-1 should match (word start)",
			sources: []string{"XRPC-1"},
			want:    []string{"XRPC-1"},
		},
		{
			name:    "source precedence: first wins, third ignored",
			sources: []string{"", "BRANCH-42", "DESC-99"},
			want:    []string{"BRANCH-42"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractTicketKeys(c.sources...)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ExtractTicketKeys(%q) = %v, want %v", c.sources, got, c.want)
			}
		})
	}
}
