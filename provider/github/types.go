package github

type ghUser struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

// ghLabel is a PR/issue label. Only the name is carried through to the UI.
type ghLabel struct {
	Name string `json:"name"`
}

type ghNotification struct {
	ID      string `json:"id"`
	Reason  string `json:"reason"`
	Subject struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Type  string `json:"type"`
	} `json:"subject"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	UpdatedAt string `json:"updated_at"`
}

type ghPull struct {
	Number             int      `json:"number"`
	Title              string   `json:"title"`
	HTMLURL            string   `json:"html_url"`
	State              string   `json:"state"` // open | closed
	Draft              bool     `json:"draft"`
	Merged             bool     `json:"merged"`
	MergeableState     string   `json:"mergeable_state"`
	UpdatedAt          string   `json:"updated_at"`
	Body               string   `json:"body"`
	User               ghUser   `json:"user"`
	Assignees          []ghUser `json:"assignees"`
	RequestedReviewers []ghUser `json:"requested_reviewers"`
	Base               struct {
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Labels []ghLabel `json:"labels"`
}

// ghSearchItem is one row of GET /search/issues; PRs carry pull_request.
type ghSearchItem struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HTMLURL     string `json:"html_url"`
	State       string `json:"state"`
	Draft       bool   `json:"draft"`
	UpdatedAt   string `json:"updated_at"`
	User        ghUser `json:"user"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	RepositoryURL string    `json:"repository_url"`
	Labels        []ghLabel `json:"labels"`
}

type ghSearchResult struct {
	Items []ghSearchItem `json:"items"`
}
