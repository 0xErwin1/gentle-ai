package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The beta channel exists so that a release candidate can be taken deliberately.
// Resolving it to the newest stable would answer a request for the beta with the
// version the user already had, and report success doing it.
func TestBetaTakesThePrereleaseAndStableSkipsIt(t *testing.T) {
	releases := `[
	  {"tag_name":"v2.4.0-rc.8","prerelease":true,"draft":false},
	  {"tag_name":"v2.3.0","prerelease":false,"draft":false}
	]`
	latest := `{"tag_name":"v2.3.0","prerelease":false,"draft":false}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			_, _ = w.Write([]byte(latest))
			return
		}
		_, _ = w.Write([]byte(releases))
	}))
	defer server.Close()

	restore := githubAPIBase
	githubAPIBase = server.URL
	defer func() { githubAPIBase = restore }()

	tool := ToolInfo{Name: "gentle-ai", Owner: "o", Repo: "r", PrereleaseChannel: true}

	stable, err := fetchLatestReleaseForTool(context.Background(), tool, false)
	if err != nil {
		t.Fatalf("stable channel: %v", err)
	}
	if stable.TagName != "v2.3.0" {
		t.Errorf("stable resolved to %q, want the newest non-prerelease", stable.TagName)
	}

	beta, err := fetchLatestReleaseForTool(context.Background(), tool, true)
	if err != nil {
		t.Fatalf("beta channel: %v", err)
	}
	if beta.TagName != "v2.4.0-rc.8" {
		t.Errorf("beta resolved to %q, want the newest release candidate", beta.TagName)
	}
}

// A tool whose beta means something else, or which has none, must not start
// taking prereleases because another tool asked for one.
func TestBetaLeavesAToolWithoutAPrereleaseChannelAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","prerelease":false,"draft":false}`))
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v2.0.0-rc.1","prerelease":true,"draft":false}]`))
	}))
	defer server.Close()

	restore := githubAPIBase
	githubAPIBase = server.URL
	defer func() { githubAPIBase = restore }()

	tool := ToolInfo{Name: "other", Owner: "o", Repo: "r"}

	resolved, err := fetchLatestReleaseForTool(context.Background(), tool, true)
	if err != nil {
		t.Fatalf("beta channel: %v", err)
	}
	if resolved.TagName != "v1.0.0" {
		t.Errorf("resolved to %q, want the stable release", resolved.TagName)
	}
}
