package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const clientTimeout = 30 * time.Second

// Config holds the credentials for the Jira Cloud enricher.
type Config struct {
	BaseURL  string
	Email    string
	APIToken string
}

// IssueResult is the data returned by a successful ticket fetch.
type IssueResult struct {
	Status   string
	Summary  string
	Assignee string
	URL      string
}

// Client fetches Jira ticket data via the Jira Cloud REST API.
type Client struct {
	cfg     Config
	http    *http.Client
	authHdr string
}

// NewClient constructs a Client for the given config.
func NewClient(cfg Config) *Client {
	creds := base64.StdEncoding.EncodeToString([]byte(cfg.Email + ":" + cfg.APIToken))
	return &Client{
		cfg:     cfg,
		http:    &http.Client{Timeout: clientTimeout},
		authHdr: "Basic " + creds,
	}
}

// Fetch retrieves the status, summary, and assignee for the given ticket key.
// A 404 returns ("", "", "", "not found"). Non-2xx returns ("", "", "", "status N").
// Any transport error is returned directly.
func (c *Client) Fetch(ctx context.Context, key string) (IssueResult, string, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s?fields=summary,status,assignee", c.cfg.BaseURL, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return IssueResult{}, "", err
	}
	req.Header.Set("Authorization", c.authHdr)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return IssueResult{}, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return IssueResult{}, "not found", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return IssueResult{}, fmt.Sprintf("status %d", resp.StatusCode), nil
	}

	var body struct {
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
			Assignee *struct {
				DisplayName string `json:"displayName"`
			} `json:"assignee"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return IssueResult{}, fmt.Sprintf("parse error: %v", err), nil
	}

	var assignee string
	if body.Fields.Assignee != nil {
		assignee = body.Fields.Assignee.DisplayName
	}
	result := IssueResult{
		Status:   body.Fields.Status.Name,
		Summary:  body.Fields.Summary,
		Assignee: assignee,
		URL:      fmt.Sprintf("%s/browse/%s", c.cfg.BaseURL, key),
	}
	return result, "", nil
}
