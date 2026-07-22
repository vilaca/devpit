package gitlab

type glUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type glTodo struct {
	ID         int    `json:"id"`
	ActionName string `json:"action_name"`
	TargetType string `json:"target_type"`
	Target     struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
	} `json:"target"`
	// GitLab nests the project under "project": {"id": ...}; there is no
	// top-level "project_id" on a todo.
	Project struct {
		ID int `json:"id"`
	} `json:"project"`
	Author    glUser `json:"author"`
	UpdatedAt string `json:"updated_at"`
}

type glMergeRequest struct {
	IID                         int      `json:"iid"`
	ProjectID                   int      `json:"project_id"`
	Title                       string   `json:"title"`
	WebURL                      string   `json:"web_url"`
	State                       string   `json:"state"` // opened | merged | closed | locked
	Draft                       bool     `json:"draft"`
	DetailedMergeStatus         string   `json:"detailed_merge_status"`
	HasConflicts                bool     `json:"has_conflicts"`
	BlockingDiscussionsResolved *bool    `json:"blocking_discussions_resolved"`
	UpdatedAt                   string   `json:"updated_at"`
	Author                      glUser   `json:"author"`
	Assignees                   []glUser `json:"assignees"`
	Reviewers                   []glUser `json:"reviewers"`
	References                  struct {
		Full string `json:"full"` // "group/project!iid"
	} `json:"references"`
}
