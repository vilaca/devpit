// Command demo-forge is a mock GitHub + GitLab host for DevPit's demo world.
//
// It impersonates both forges well enough for the real providers
// (provider/github, provider/gitlab) to run a full reconcile + fast-poll +
// GraphQL-join cycle against it, serving hand-editable JSON fixtures. It is the
// permanent source of the hero screenshot and every UI demo — never a real
// instance. See scripts/demo/README.md for how to extend the demo world.
//
// It speaks only HTTP and reads only stdlib + JSON on disk: it imports no
// internal DevPit package, so it can neither drift with the providers nor leak
// into the shipped binary (.go-arch-lint.yml keeps that edge honest).
//
// Routing. One listener serves both forges under distinct path namespaces so a
// single config can point both connections here:
//
//	github base_url: http://<addr>/gh   → REST at /gh/api/v3, GraphQL at /gh/api/graphql
//	gitlab base_url: http://<addr>/gl   → REST at /gl/api/v4, GraphQL at /gl/api/graphql
//
// (The providers derive those sub-paths from base_url themselves — GitHub
// appends /api/v3, GitLab /api/v4, both use /api/graphql.)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "localhost:9099", "listen address")
	fixtures := flag.String("fixtures", "", "path to the fixtures directory (required)")
	flag.Parse()

	if *fixtures == "" {
		log.Fatal("demo-forge: -fixtures is required")
	}
	if _, err := os.Stat(*fixtures); err != nil {
		log.Fatalf("demo-forge: fixtures dir: %v", err)
	}

	s := &server{root: *fixtures}
	mux := http.NewServeMux()
	mux.Handle("/gh/", http.StripPrefix("/gh", logging(s.github)))
	mux.Handle("/gl/", http.StripPrefix("/gl", logging(s.gitlab)))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "demo-forge: use /gh (github) or /gl (gitlab)", http.StatusNotFound)
	})

	log.Printf("demo-forge serving fixtures from %s on http://%s", *fixtures, *addr)
	//nolint:gosec // demo tool bound to localhost; no timeouts needed.
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("demo-forge: %v", err)
	}
}

type server struct{ root string }

// --- GitHub -----------------------------------------------------------------

func (s *server) github(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/v3/user":
		s.serve(w, "github/user.json")
	case p == "/api/v3/notifications":
		// Fast-poll sends conditional headers; we ignore them and always 200,
		// but echo validators so the provider stores a cursor (ADR-0018).
		w.Header().Set("ETag", `"demo-notifications"`)
		w.Header().Set("Last-Modified", "Mon, 13 Jul 2026 00:00:00 GMT")
		s.serve(w, "github/notifications.json")
	case p == "/api/v3/search/issues":
		s.serve(w, "github/search_"+ghSearchScope(r.URL.Query().Get("q"))+".json")
	case p == "/api/graphql":
		s.graphql(w, r, "github/graphql.json", ghAliasRE, "pullRequest")
	case strings.HasPrefix(p, "/api/v3/repos/"):
		s.githubRepos(w, strings.TrimPrefix(p, "/api/v3/repos/"))
	default:
		notFound(w, "github", p)
	}
}

// githubRepos handles /repos/{owner}/{repo}/{pulls/{n}|collaborators}.
func (s *server) githubRepos(w http.ResponseWriter, sub string) {
	parts := strings.Split(sub, "/")
	switch {
	case len(parts) == 4 && parts[2] == "pulls":
		s.serve(w, fmt.Sprintf("github/pulls/%s~%s~%s.json", parts[0], parts[1], parts[3]))
	case len(parts) == 3 && parts[2] == "collaborators":
		s.serve(w, fmt.Sprintf("github/collaborators/%s~%s.json", parts[0], parts[1]))
	default:
		notFound(w, "github repos", sub)
	}
}

// ghSearchScope maps a search `q=` string to its fixture suffix by the role
// qualifier the reconcile builds (reconcile.go): review-requested / assignee /
// author / user (sole-approver probe).
func ghSearchScope(q string) string {
	for _, s := range []string{"review-requested", "assignee", "author", "user"} {
		if strings.Contains(q, s+":") {
			return s
		}
	}
	return "author" // safe default; unknown scope shouldn't happen
}

// --- GitLab -----------------------------------------------------------------

func (s *server) gitlab(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/v4/user":
		s.serve(w, "gitlab/user.json")
	case p == "/api/v4/todos":
		s.serve(w, "gitlab/todos.json")
	case p == "/api/v4/merge_requests":
		s.serve(w, "gitlab/merge_requests_"+glMRScope(r.URL.Query())+".json")
	case p == "/api/graphql":
		s.graphql(w, r, "gitlab/graphql.json", glAliasRE, "mergeRequest")
	case p == "/api/v4/projects":
		s.serve(w, "gitlab/projects.json")
	case strings.HasPrefix(p, "/api/v4/projects/"):
		s.gitlabProjects(w, r)
	default:
		notFound(w, "gitlab", p)
	}
}

// gitlabProjects handles the /projects/{id|path}/... subtree. It reads the
// ESCAPED path so a URL-encoded project path (group%2Fproject) stays one
// segment (Go decodes %2F in r.URL.Path otherwise).
func (s *server) gitlabProjects(w http.ResponseWriter, r *http.Request) {
	esc := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4/projects/")
	parts := strings.Split(esc, "/")
	switch {
	case len(parts) == 2 && parts[1] == "merge_requests":
		s.serve(w, fmt.Sprintf("gitlab/projects/%s_merge_requests.json", parts[0]))
	case len(parts) == 3 && parts[1] == "merge_requests":
		s.serve(w, fmt.Sprintf("gitlab/projects/%s_mr_%s.json", parts[0], parts[2]))
	case len(parts) == 3 && parts[1] == "members" && parts[2] == "all":
		path, _ := url.PathUnescape(parts[0])
		s.serve(w, fmt.Sprintf("gitlab/project_members/%s.json", strings.ReplaceAll(path, "/", "~")))
	default:
		notFound(w, "gitlab projects", esc)
	}
}

// glMRScope maps the /merge_requests query to its fixture suffix. The reviewer
// sweep is scope=all&reviewer_username=…; the other two are scope=… alone.
func glMRScope(q url.Values) string {
	if q.Get("reviewer_username") != "" {
		return "reviewer"
	}
	return q.Get("scope") // assigned_to_me | created_by_me
}

// --- GraphQL ----------------------------------------------------------------

var (
	// a0:repository(owner:"acme",name:"checkout-api"){pullRequest(number:42){…
	ghAliasRE = regexp.MustCompile(`(a\d+):repository\(owner:"([^"]+)",name:"([^"]+)"\)\{pullRequest\(number:(\d+)\)`)
	// a0:project(fullPath:"platform/auth"){mergeRequest(iid:"7"){…
	glAliasRE = regexp.MustCompile(`(a\d+):project\(fullPath:"([^"]+)"\)\{mergeRequest\(iid:"([^"]+)"\)`)
)

// graphql answers a batched join POST. It parses each aliased sub-query for the
// item it names, looks that item's facts up in the fixture map (keyed by
// "owner/repo#number" for GitHub, "group/project!iid" for GitLab), and rebuilds
// the aliased response the provider expects. An item absent from the fixture
// yields a null node — the exact graceful-degradation shape the join tolerates.
func (s *server) graphql(w http.ResponseWriter, r *http.Request, fixture string, re *regexp.Regexp, wrapKey string) {
	body, err := s.read(fixture)
	if err != nil {
		http.Error(w, "no graphql fixture", http.StatusInternalServerError)
		return
	}
	var facts map[string]json.RawMessage
	if err := json.Unmarshal(body, &facts); err != nil {
		http.Error(w, "bad graphql fixture", http.StatusInternalServerError)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad graphql request", http.StatusBadRequest)
		return
	}

	data := map[string]any{}
	for _, m := range re.FindAllStringSubmatch(req.Query, -1) {
		alias := m[1]
		// GitHub splits into owner/name/number (→ "owner/name#number");
		// GitLab captures fullPath and iid (→ "group/project!iid").
		var key string
		if wrapKey == "pullRequest" {
			key = m[2] + "/" + m[3] + "#" + m[4]
		} else {
			key = m[2] + "!" + m[3]
		}
		node := json.RawMessage("null")
		if v, ok := facts[key]; ok {
			node = v
		}
		data[alias] = map[string]json.RawMessage{wrapKey: node}
	}
	writeJSON(w, map[string]any{"data": data})
}

// --- helpers ----------------------------------------------------------------

// serve writes a fixture file as JSON, 404 if it is missing so a gap in the
// demo world is visible rather than silently empty. Timestamp templates are
// expanded first (see relativeTimes).
func (s *server) serve(w http.ResponseWriter, rel string) {
	b, err := s.read(rel)
	if err != nil {
		notFound(w, "fixture", rel)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// read loads a fixture and expands its timestamp templates.
func (s *server) read(rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(s.root, filepath.Clean(rel)))
	if err != nil {
		return nil, err
	}
	return relativeTimes(b), nil
}

// nowTemplateRE matches {{now}} and {{now-<N><d|h|m>}} (e.g. {{now-14d}}).
var nowTemplateRE = regexp.MustCompile(`\{\{now(?:-(\d+)([dhm]))?\}\}`)

// startedAt is the reference time for {{now-…}} expansion, fixed once at process
// start. It MUST NOT be time.Now() per request: a fixture served twice (every
// fast-poll re-reads notifications.json) would then carry a drifting updated_at,
// which changes the provider's signal dedupe key and inflates the ×N mention
// count each cycle. A per-run constant keeps ages stable within a run and still
// fresh across runs (each run.sh restarts the forge).
var startedAt = time.Now().UTC()

// relativeTimes rewrites {{now-…}} tokens to RFC3339 timestamps relative to the
// process start, so fixtures pin ages to "N days ago" and the fresh/stale/old
// bands never rot as the calendar advances (handoff 4).
func relativeTimes(b []byte) []byte {
	now := startedAt
	return nowTemplateRE.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := nowTemplateRE.FindSubmatch(m)
		t := now
		if len(sub[1]) > 0 {
			n, _ := strconv.Atoi(string(sub[1]))
			unit := map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute}[string(sub[2])]
			t = now.Add(-time.Duration(n) * unit)
		}
		return []byte(t.Format(time.RFC3339))
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	//nolint:errchkjson // v is a map of json.RawMessage; encoding cannot fail, and a write error to the client is moot.
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, kind, what string) {
	//nolint:gosec // G706: demo tool logging a localhost request path; not a security surface.
	log.Printf("demo-forge: unmapped %s request: %s", kind, what)
	http.Error(w, fmt.Sprintf("demo-forge: no fixture for %s %q", kind, what), http.StatusNotFound)
}

func logging(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // G706: demo tool request log; localhost-only, not a security surface.
		log.Printf("%s %s", r.Method, r.URL.RequestURI())
		next(w, r)
	}
}
