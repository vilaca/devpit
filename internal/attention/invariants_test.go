package attention

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vilaca/devpit/sdk"
)

// TestSignalRankingRatchet (INV-5): rankingTime sweeps every signal.* event
// into an item's ranking clock, so a signal added to sdk moves items the day it
// lands. Every sdk signal type must be classified here against the ADR-0016
// rule that sanctions its effect on ranking; an unclassified one fails, making
// that effect a decision rather than a side effect of the signal.* prefix.
func TestSignalRankingRatchet(t *testing.T) {
	sanctioned := map[string]string{
		sdk.SignalMentioned:        "attention signal — ADR-0016 Ranking (age band then recency)",
		sdk.SignalReviewRequested:  "attention signal — ADR-0016 Ranking (age band then recency)",
		sdk.SignalReviewSubmitted:  "attention signal — ADR-0016 Ranking (age band then recency)",
		sdk.SignalAssigned:         "attention signal — ADR-0016 Ranking (age band then recency)",
		sdk.SignalApproved:         "rank-only — ADR-0016 Review verdicts advance the ranking clock",
		sdk.SignalChangesRequested: "rank-only — ADR-0016 Review verdicts advance the ranking clock",
		sdk.SignalCIFailed:         "rank-only, suppressed once old — ADR-0016 2026-08-07 amendment",
	}

	src := readGoSources(t, repoRoot(t), "sdk")
	constants := regexp.MustCompile(`=\s*"(signal\.[a-z_]+)"`).FindAllStringSubmatch(src, -1)
	if len(constants) == 0 {
		t.Fatal("found no signal.* constants in sdk — the scan is broken")
	}
	for _, m := range constants {
		if _, ok := sanctioned[m[1]]; !ok {
			t.Errorf("%s is an sdk signal with no ranking sanction, but rankingTime will move items by it: "+
				"classify it here against ADR-0016, or keep it out of the signal.* namespace", m[1])
		}
	}
}

// TestReadOnlyOutbound (INV-1): DevPit never writes to a forge or to Jira. In
// provider and Jira code every outbound request is a GET or HEAD, except each
// provider's one GraphQL POST, which carries queries only — no mutation
// document anywhere. A request method must be an http.Method constant, so a
// write can't hide behind a variable.
func TestReadOnlyOutbound(t *testing.T) {
	graphQLPost := map[string]int{ // file → count of its GraphQL query POST (want 1)
		"provider/github/graphql.go": 0,
		"provider/gitlab/graphql.go": 0,
	}
	methodUse := regexp.MustCompile(
		`http\.Method[A-Z][a-z]+\b|http\.(?:Post|PostForm)\(|"(?:POST|PUT|PATCH|DELETE)"`)
	methodArg := regexp.MustCompile(
		`http\.NewRequestWithContext\([^,]+,\s*([^,]+),|http\.NewRequest\(\s*([^,]+),`)
	mutation := regexp.MustCompile(`\bmutation\b`)

	root := repoRoot(t)
	for _, dir := range []string{"provider/github", "provider/gitlab", "internal/jira"} {
		for rel, src := range goSourcesByFile(t, root, dir) {
			for _, m := range methodUse.FindAllString(src, -1) {
				switch _, isGraphQL := graphQLPost[rel]; {
				case m == "http.MethodGet" || m == "http.MethodHead":
				case m == "http.MethodPost" && isGraphQL:
					graphQLPost[rel]++
				default:
					t.Errorf("%s: %s — DevPit is read-only (INV-1, ADR-0017)", rel, m)
				}
			}
			for _, m := range methodArg.FindAllStringSubmatch(src, -1) {
				arg := strings.TrimSpace(m[1] + m[2])
				if !strings.HasPrefix(arg, "http.Method") {
					t.Errorf("%s: request method %q is not an http.Method constant", rel, arg)
				}
			}
			if mutation.MatchString(src) {
				t.Errorf("%s: contains a GraphQL mutation — DevPit is read-only (INV-1, ADR-0017)", rel)
			}
		}
	}
	for rel, n := range graphQLPost {
		if n != 1 {
			t.Errorf("%s: want its one GraphQL query POST, found %d — if the call moved, update this test", rel, n)
		}
	}
}

// goSourcesByFile returns the non-test *.go files directly under dir
// (relative to root), keyed by their slash-separated path relative to root.
func goSourcesByFile(t *testing.T, root, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		t.Fatalf("goSourcesByFile: ReadDir %q: %v", dir, err)
	}
	files := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, dir, e.Name()))
		if err != nil {
			t.Fatalf("goSourcesByFile: ReadFile %q: %v", e.Name(), err)
		}
		files[dir+"/"+e.Name()] = string(data)
	}
	return files
}
