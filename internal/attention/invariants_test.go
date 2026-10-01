package attention

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vilaca/devpit/sdk"
)

// TestSignalRankingRatchet (INV-5): rankingTime sweeps every signal.* event
// into an item's ranking clock, so a signal added to sdk moves items the day it
// lands. Every sdk signal type must be classified here against the ADR-0016
// rule that sanctions its effect on ranking; an unclassified one fails, making
// that effect a decision rather than a side effect of the signal.* prefix.
// (TestEventVocabRatchet keeps signal types from being minted outside sdk.)
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

	found := 0
	for name, value := range sdkStringConstants(t, repoRoot(t)) {
		if !strings.HasPrefix(value, sdk.SignalPrefix) || value == sdk.SignalPrefix {
			continue
		}
		found++
		if _, ok := sanctioned[value]; !ok {
			t.Errorf("sdk.%s = %q is a signal with no ranking sanction, but rankingTime will move items by it: "+
				"classify it here against ADR-0016, or keep it out of the signal.* namespace", name, value)
		}
	}
	if found == 0 {
		t.Fatal("found no signal.* constants in sdk — the scan is broken")
	}
}

// sdkStringConstants evaluates sdk's package-level string constants and vars,
// following identifiers, conversions, and `+`, so `SignalPrefix + "merged"` is
// seen as "signal.merged" however the declaration is spelled.
func sdkStringConstants(t *testing.T, root string) map[string]string {
	t.Helper()
	exprs := map[string]ast.Expr{}
	fset := token.NewFileSet()
	for rel := range goSourcesUnder(t, root, nil, "sdk") {
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		collectValueExprs(f, exprs)
	}
	values := make(map[string]string, len(exprs))
	for name, e := range exprs {
		if s, ok := evalString(e, exprs, 0); ok {
			values[name] = s
		}
	}
	return values
}

// collectValueExprs records each package-level const/var name's initialiser.
func collectValueExprs(f *ast.File, exprs map[string]ast.Expr) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					exprs[name.Name] = vs.Values[i]
				}
			}
		}
	}
}

// evalString evaluates a string-valued constant expression over exprs.
func evalString(e ast.Expr, exprs map[string]ast.Expr, depth int) (string, bool) {
	if depth > 32 {
		return "", false
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.Ident:
		if x, ok := exprs[e.Name]; ok {
			return evalString(x, exprs, depth+1)
		}
	case *ast.ParenExpr:
		return evalString(e.X, exprs, depth+1)
	case *ast.CallExpr: // a conversion such as EventType("signal.x")
		if len(e.Args) == 1 {
			return evalString(e.Args[0], exprs, depth+1)
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			l, okL := evalString(e.X, exprs, depth+1)
			r, okR := evalString(e.Y, exprs, depth+1)
			return l + r, okL && okR
		}
	}
	return "", false
}

// TestReadOnlyOutbound (INV-1): DevPit never writes to a forge or to Jira.
// Outside the inbound server packages, every outbound request is a GET or
// HEAD, except each provider's one GraphQL POST (whose doGraphQL refuses any
// non-query document at runtime), and no source holds a mutation. A request
// method must be an http.Method constant, so a write can't hide behind a
// variable or a hand-built *http.Request.
func TestReadOnlyOutbound(t *testing.T) {
	graphQLPost := map[string]int{ // file → count of its GraphQL query POST (want 1)
		"provider/github/graphql.go": 0,
		"provider/gitlab/graphql.go": 0,
	}
	// The inbound server answers the SPA's PUT/DELETE on the local handle_next
	// flag; it makes no outbound calls, and is the one place write verbs belong.
	inbound := map[string]bool{"internal/api": true, "internal/web": true}

	methodUse := regexp.MustCompile(
		`http\.Method[A-Z][a-z]+\b|\.(?:Post|PostForm)\(|"(?:POST|PUT|PATCH|DELETE)"`)
	methodArg := regexp.MustCompile(
		`http\.NewRequestWithContext\([^,]+,\s*([^,]+),|http\.NewRequest\(\s*([^,]+),`)
	methodSet := regexp.MustCompile(`\.Method\s*=[^=]|\bMethod:\s`)
	// A mutation document ("mutation {", "mutation Name("), not the word.
	mutation := regexp.MustCompile(`\bmutation\s*[{(]|\bmutation\s+[A-Za-z_]\w*\s*[{(]`)

	for rel, src := range goSourcesUnder(t, repoRoot(t), inbound, "provider", "internal", "sdk") {
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
			if arg := strings.TrimSpace(m[1] + m[2]); !strings.HasPrefix(arg, "http.Method") {
				t.Errorf("%s: request method %q is not an http.Method constant", rel, arg)
			}
		}
		if m := methodSet.FindString(src); m != "" {
			t.Errorf("%s: sets a request method directly (%q) — use http.NewRequestWithContext with a constant", rel, m)
		}
		if mutation.MatchString(src) {
			t.Errorf("%s: contains a GraphQL mutation — DevPit is read-only (INV-1, ADR-0017)", rel)
		}
	}
	for rel, n := range graphQLPost {
		if n != 1 {
			t.Errorf("%s: want its one GraphQL query POST, found %d — if the call moved, update this test", rel, n)
		}
	}
}

// goSourcesUnder returns the non-test *.go files under each dir (relative to
// root, recursively, skipping any dir in skip), keyed by slash-separated path
// relative to root.
func goSourcesUnder(t *testing.T, root string, skip map[string]bool, dirs ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if skip[rel] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = string(data)
			return nil
		})
		if err != nil {
			t.Fatalf("goSourcesUnder %q: %v", dir, err)
		}
	}
	return files
}
