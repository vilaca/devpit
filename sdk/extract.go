package sdk

import "regexp"

// ticketKeyRe matches Jira-style ticket keys: one uppercase letter followed by
// one or more uppercase letters or digits, a hyphen, and one or more digits
// (e.g. RPC-74504, PROJ-1). Word-bounded to avoid matching inside larger tokens.
var ticketKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)

// ExtractTicketKeys extracts Jira ticket keys from the given sources.
// Sources are evaluated in order; the first source with at least one match
// wins — its keys are returned (deduplicated, first-seen order) and the
// remaining sources are ignored. Returns nil when no source yields a match.
func ExtractTicketKeys(sources ...string) []string {
	for _, src := range sources {
		found := ticketKeyRe.FindAllString(src, -1)
		if len(found) == 0 {
			continue
		}
		seen := make(map[string]bool, len(found))
		keys := make([]string, 0, len(found))
		for _, k := range found {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		return keys
	}
	return nil
}
