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
			sources: []string{"PROJ-74504: Add feature", "proj-feature", "PROJ-99999 in description"},
			want:    []string{"PROJ-74504"},
		},
		{
			name:    "skip empty first source, match in second",
			sources: []string{"no keys here", "feature/PROJ-100-my-branch"},
			want:    []string{"PROJ-100"},
		},
		{
			name:    "multiple keys in winning source, deduplicated",
			sources: []string{"PROJ-1 and PROJ-2 and PROJ-1 again"},
			want:    []string{"PROJ-1", "PROJ-2"},
		},
		{
			name:    "placeholder PROJ-XXXXX must not match",
			sources: []string{"PROJ-XXXXX is not a ticket"},
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
			sources: []string{"proj-123"},
			want:    nil,
		},
		{
			name:    "word boundary: XPROJ-1 should match (word start)",
			sources: []string{"XPROJ-1"},
			want:    []string{"XPROJ-1"},
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
