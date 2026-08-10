package attention

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/vilaca/devpit/sdk"
)

// repoRoot resolves the repository root from this test file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is .../internal/attention/sdk_surface_test.go — two levels up.
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// readGoSources returns the concatenated content of all non-test *.go files
// under the given directories (relative to root).
func readGoSources(t *testing.T, root string, dirs ...string) string {
	t.Helper()
	var sb strings.Builder
	for _, dir := range dirs {
		abs := filepath.Join(root, dir)
		entries, err := os.ReadDir(abs)
		if err != nil {
			t.Fatalf("readGoSources: ReadDir %q: %v", abs, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(abs, e.Name()))
			if err != nil {
				t.Fatalf("readGoSources: ReadFile %q: %v", e.Name(), err)
			}
			sb.Write(data)
		}
	}
	return sb.String()
}

// TestSDKSurfaceRatchet (INV-4): every field of sdk.ItemObservedPayload must
// appear in at least one producer (provider/github or provider/gitlab) AND at
// least one consumer (internal/attention or internal/api). Fields in the
// carveOut map are sanctioned pre-wired exceptions (roadmap forward-deps).
func TestSDKSurfaceRatchet(t *testing.T) {
	// carveOut maps field name → Roadmap link for sanctioned exceptions.
	// Empty today: all ItemObservedPayload fields are produced and consumed.
	carveOut := map[string]string{}

	root := repoRoot(t)
	producerSrc := readGoSources(t, root, "provider/github", "provider/gitlab")
	consumerSrc := readGoSources(t, root, "internal/attention", "internal/api")

	typ := reflect.TypeFor[sdk.ItemObservedPayload]()
	for field := range typ.Fields() {
		field := field.Name
		if _, ok := carveOut[field]; ok {
			continue
		}
		// Word-boundary match: covers pl.Field, Field:, and f.Field usages.
		pattern := `\b` + regexp.QuoteMeta(field) + `\b`
		re := regexp.MustCompile(pattern)

		if !re.MatchString(producerSrc) {
			t.Errorf("sdk.ItemObservedPayload.%s: not referenced in any producer (provider/github, provider/gitlab)", field)
		}
		if !re.MatchString(consumerSrc) {
			t.Errorf("sdk.ItemObservedPayload.%s: not referenced in any consumer (internal/attention, internal/api)", field)
		}
	}
}

// TestEventVocabRatchet (item 5): asserts that no raw event-type string literal
// appears in non-test *.go files outside sdk/. All event types must use the
// sdk constants (sdk.EventItemObserved, sdk.SignalMentioned, etc.).
func TestEventVocabRatchet(t *testing.T) {
	root := repoRoot(t)

	// Collect all non-test *.go files outside sdk/.
	var sources []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip vendor-like dirs and the sdk package itself (the source of truth).
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if d.Name() == ".claude" || d.Name() == "vendor" || rel == "sdk" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		sources = append(sources, path)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	// Patterns that match bare event-type string literals. Each pattern is
	// anchored with a leading " so it matches only inside a Go string literal.
	bare := []*regexp.Regexp{
		regexp.MustCompile(`"item\.observed"`),
		regexp.MustCompile(`"item\.removed"`),
		regexp.MustCompile(`"signal\.[a-z_]+"`),
	}

	for _, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %q: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, re := range bare {
			if re.Match(data) {
				t.Errorf("%s: contains bare event-type literal matching %s — use sdk constants instead", rel, re)
			}
		}
	}
}
