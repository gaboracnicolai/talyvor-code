package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRepoFromURL_HTTPS(t *testing.T) {
	owner, repo, err := ParseRepoFromURL("https://github.com/acme/widgets.git")
	if err != nil {
		t.Fatalf("ParseRepoFromURL: %v", err)
	}
	if owner != "acme" || repo != "widgets" {
		t.Fatalf("got owner=%q repo=%q", owner, repo)
	}
}

func TestParseRepoFromURL_HTTPSWithoutDotGit(t *testing.T) {
	owner, repo, err := ParseRepoFromURL("https://github.com/acme/widgets")
	if err != nil {
		t.Fatalf("ParseRepoFromURL: %v", err)
	}
	if owner != "acme" || repo != "widgets" {
		t.Fatalf("got owner=%q repo=%q", owner, repo)
	}
}

func TestParseRepoFromURL_SSH(t *testing.T) {
	owner, repo, err := ParseRepoFromURL("git@github.com:acme/widgets.git")
	if err != nil {
		t.Fatalf("ParseRepoFromURL: %v", err)
	}
	if owner != "acme" || repo != "widgets" {
		t.Fatalf("got owner=%q repo=%q", owner, repo)
	}
}

func TestParseRepoFromURL_SSHWithoutDotGit(t *testing.T) {
	owner, repo, err := ParseRepoFromURL("git@github.com:acme/widgets")
	if err != nil {
		t.Fatalf("ParseRepoFromURL: %v", err)
	}
	if owner != "acme" || repo != "widgets" {
		t.Fatalf("got owner=%q repo=%q", owner, repo)
	}
}

func TestParseRepoFromURL_RejectsNonGitHub(t *testing.T) {
	_, _, err := ParseRepoFromURL("git@gitlab.com:acme/widgets.git")
	if err == nil {
		t.Fatal("expected error for non-GitHub remote")
	}
	_, _, err = ParseRepoFromURL("https://gitea.example.com/acme/widgets.git")
	if err == nil {
		t.Fatal("expected error for non-GitHub https remote")
	}
}

func TestParseRepoFromURL_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"github.com",
		"https://github.com/onlyone",
		"git@github.com:onlyone",
	} {
		if _, _, err := ParseRepoFromURL(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestGeneratePRBody_IncludesIssueAndCost(t *testing.T) {
	body := GeneratePRBody(
		"ENG-42",
		"Add JWT authentication",
		"Wire JWT verifier into the Express middleware chain.",
		[]string{"src/auth/jwt.ts", "src/server.ts"},
		0.1234,
	)
	for _, want := range []string{
		"## Summary",
		"ENG-42 — Add JWT authentication",
		"Wire JWT verifier",
		"src/auth/jwt.ts",
		"src/server.ts",
		"## AI Cost Attribution",
		"$0.12",
		"attributed to ENG-42",
		"Opened by [Talyvor Code]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestGeneratePRBody_NoIssueOmitsAttribution(t *testing.T) {
	body := GeneratePRBody("", "", "Refactor", []string{"a.go"}, 0.01)
	if strings.Contains(body, "attributed to") {
		t.Errorf("should not claim attribution without issue: %s", body)
	}
}

func TestCreatePR_PostsCorrectBodyAndHeaders(t *testing.T) {
	var gotPath string
	var gotMethod string
	var gotAuth, gotAccept, gotAPIVersion string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotAPIVersion = r.Header.Get("X-Github-Api-Version")
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/widgets/pull/7","title":"feat: x","state":"open"}`))
	}))
	defer srv.Close()

	res, err := createPRWithBase(context.Background(), srv.URL, "tlv_gh", PRConfig{
		Owner: "acme",
		Repo:  "widgets",
		Title: "feat: x",
		Body:  "body",
		Head:  "feature/x",
		Base:  "main",
		Draft: false,
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/repos/acme/widgets/pulls") {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tlv_gh" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("accept = %q", gotAccept)
	}
	if gotAPIVersion != "2022-11-28" {
		t.Errorf("api version = %q", gotAPIVersion)
	}
	if gotBody["title"] != "feat: x" || gotBody["head"] != "feature/x" || gotBody["base"] != "main" {
		t.Errorf("body = %+v", gotBody)
	}
	if res.Number != 7 || res.URL == "" {
		t.Errorf("result = %+v", res)
	}
}

func TestCreatePR_RequiresToken(t *testing.T) {
	_, err := CreatePR(context.Background(), "", PRConfig{Owner: "a", Repo: "b", Head: "h", Base: "main"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "token") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestSlugifyBranch_HandlesCommonInputs(t *testing.T) {
	cases := map[string]string{
		"Add JWT authentication":           "feat/add-jwt-authentication",
		"Fix login bug on Safari":          "fix/fix-login-bug-on-safari",
		"Refactor database layer":          "feat/refactor-database-layer",
		"Bug: race condition in scheduler": "fix/bug-race-condition-in-scheduler",
		"Hotfix payment regression":        "fix/hotfix-payment-regression",
	}
	for in, want := range cases {
		if got := SlugifyBranch(in); got != want {
			t.Errorf("SlugifyBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugifyBranch_StripsSpecialCharsAndSqueezes(t *testing.T) {
	got := SlugifyBranch("Add !!! JWT @@@ auth ### now")
	// Special chars collapse into single hyphens; result is
	// alphanumeric/hyphen only.
	if strings.ContainsAny(got, "!@#") {
		t.Fatalf("special chars survived: %q", got)
	}
	if strings.Contains(got, "--") {
		t.Fatalf("dashes not squeezed: %q", got)
	}
}

func TestSlugifyBranch_RespectsMaxLength(t *testing.T) {
	long := strings.Repeat("add jwt auth ", 20)
	got := SlugifyBranch(long)
	if len(got) > MaxBranchSlugLen {
		t.Fatalf("len(%q) = %d, want ≤ %d", got, len(got), MaxBranchSlugLen)
	}
}

func TestSlugifyBranch_EmptyReturnsTimestampedFallback(t *testing.T) {
	got := SlugifyBranch("")
	if !strings.HasPrefix(got, "feat/talyvor-") {
		t.Fatalf("expected feat/talyvor-<ts>, got %q", got)
	}
}

func TestCreatePR_BubblesGitHubErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	_, err := createPRWithBase(context.Background(), srv.URL, "tlv_gh", PRConfig{
		Owner: "acme", Repo: "widgets", Title: "t", Head: "h", Base: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("expected 422 surfaced, got %v", err)
	}
}
