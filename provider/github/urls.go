package github

import (
	"strconv"
	"strings"
)

// parsePullURL extracts owner, repo, number from a REST PR URL such as
// "https://api.github.com/repos/acme/api/pulls/42".
func parsePullURL(u string) (owner, repo string, number int, ok bool) {
	_, after, ok := strings.Cut(u, "/repos/")
	if !ok {
		return "", "", 0, false
	}
	parts := strings.Split(strings.Trim(after, "/"), "/")
	if len(parts) < 4 || parts[2] != "pulls" {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", "", 0, false
	}
	return parts[0], parts[1], n, true
}

// repoFromSearchItem derives "owner/repo" from a search item's repository_url
// ("https://api.github.com/repos/acme/api").
func repoFromSearchItem(it ghSearchItem) string {
	i := strings.Index(it.RepositoryURL, "/repos/")
	if i < 0 {
		return ""
	}
	return strings.Trim(it.RepositoryURL[i+len("/repos/"):], "/")
}
