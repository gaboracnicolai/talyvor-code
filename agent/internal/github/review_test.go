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

// ─── GetOpenPR ─────────────────────────────────────

func TestGetOpenPR_ReturnsNumberFromQuery(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"number":42,"html_url":"https://github.com/acme/widgets/pull/42"}]`))
	}))
	defer srv.Close()

	num, err := getOpenPRWithBase(context.Background(), srv.URL, "tlv_gh", "acme", "widgets", "feature/x")
	if err != nil {
		t.Fatalf("GetOpenPR: %v", err)
	}
	if num != 42 {
		t.Fatalf("number = %d, want 42", num)
	}
	if !strings.HasSuffix(gotPath, "/repos/acme/widgets/pulls") {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "state=open") {
		t.Errorf("query missing state=open: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "acme%3Afeature%2Fx") &&
		!strings.Contains(gotQuery, "acme:feature/x") {
		t.Errorf("query missing head=owner:branch: %q", gotQuery)
	}
}

func TestGetOpenPR_NoMatchReturnsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	_, err := getOpenPRWithBase(context.Background(), srv.URL, "tlv_gh", "acme", "widgets", "feature/x")
	if err == nil || !strings.Contains(err.Error(), "no open PR") {
		t.Fatalf("expected no-open-PR error, got %v", err)
	}
}

// ─── PostPRReview ───────────────────────────────────

func TestPostPRReview_PostsCommentEvent(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":99}`))
	}))
	defer srv.Close()

	if err := postPRReviewWithBase(context.Background(), srv.URL, "tlv_gh", "acme", "widgets", 42, "## Review\nLooks good."); err != nil {
		t.Fatalf("PostPRReview: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/repos/acme/widgets/pulls/42/reviews") {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tlv_gh" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody["event"] != "COMMENT" {
		t.Errorf("event = %v, want COMMENT", gotBody["event"])
	}
	if !strings.Contains(gotBody["body"].(string), "Looks good") {
		t.Errorf("body missing review text: %v", gotBody["body"])
	}
}

func TestPostPRReview_RequiresToken(t *testing.T) {
	if err := PostPRReview(context.Background(), "", "a", "b", 1, "x"); err == nil {
		t.Fatal("expected token error")
	}
}

// ─── GetPRFiles ─────────────────────────────────────

func TestGetPRFiles_ReturnsFilenames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/acme/widgets/pulls/7/files") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{"filename":"src/auth/jwt.ts"},
			{"filename":"src/server.ts"}
		]`))
	}))
	defer srv.Close()
	files, err := getPRFilesWithBase(context.Background(), srv.URL, "tlv_gh", "acme", "widgets", 7)
	if err != nil {
		t.Fatalf("GetPRFiles: %v", err)
	}
	if len(files) != 2 || files[0] != "src/auth/jwt.ts" {
		t.Fatalf("files = %+v", files)
	}
}

// ─── TruncateDiff ──────────────────────────────────

func TestTruncateDiff_PassThroughWhenSmall(t *testing.T) {
	diff := strings.Repeat("a", 100)
	if got := TruncateDiff(diff, 200); got != diff {
		t.Fatal("small diff should pass through unchanged")
	}
}

func TestTruncateDiff_TruncatesWithMarker(t *testing.T) {
	diff := strings.Repeat("a", 1000) + "MIDDLE" + strings.Repeat("z", 1000)
	got := TruncateDiff(diff, 200)
	if !strings.Contains(got, "[diff truncated") {
		t.Fatalf("missing truncation marker:\n%s", got)
	}
	if len(got) > 400 {
		t.Fatalf("output too large: %d", len(got))
	}
	// The head and tail of the input should both be present.
	if !strings.HasPrefix(got, "aaa") {
		t.Fatalf("missing head: %s", got[:20])
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), strings.Repeat("z", 50)) {
		t.Fatalf("missing tail")
	}
}

func TestMaxDiffChars_Constant(t *testing.T) {
	// Spec pins this at 32000 chars (~8000 tokens).
	if MaxDiffChars != 32000 {
		t.Fatalf("MaxDiffChars = %d, want 32000", MaxDiffChars)
	}
}

// ─── ExtractVerdict ────────────────────────────────

func TestExtractVerdict_FindsApprove(t *testing.T) {
	text := `## Verdict
APPROVE
`
	got := ExtractVerdict(text)
	if got != "APPROVE" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractVerdict_FindsRequestChanges(t *testing.T) {
	for _, text := range []string{
		"## Verdict\nREQUEST CHANGES\n",
		"Verdict: REQUEST CHANGES — there are two critical issues",
	} {
		if got := ExtractVerdict(text); got != "REQUEST CHANGES" {
			t.Errorf("got %q for %q", got, text)
		}
	}
}

func TestExtractVerdict_FindsNeedsDiscussion(t *testing.T) {
	got := ExtractVerdict("## Verdict\nNEEDS DISCUSSION about the migration\n")
	if got != "NEEDS DISCUSSION" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractVerdict_DefaultsToNeedsDiscussion(t *testing.T) {
	got := ExtractVerdict("no verdict in this body")
	if got != "NEEDS DISCUSSION" {
		t.Fatalf("default = %q", got)
	}
}

// ─── CountFindings ────────────────────────────────

func TestCountFindings_CountsCriticalAndWarning(t *testing.T) {
	review := `## Review

### 🔴 Critical Issues

- SQL injection in auth.go:42
- Hardcoded token in config.go:13

### 🟡 Warnings

- N+1 query in middleware.go:88
- Missing error handling

### 💡 Suggestions

- Rename helper for clarity
`
	c, w := CountFindings(review)
	if c != 2 {
		t.Errorf("critical = %d, want 2", c)
	}
	if w != 2 {
		t.Errorf("warning = %d, want 2", w)
	}
}

func TestCountFindings_NoneSectionIsZero(t *testing.T) {
	review := `### 🔴 Critical Issues
None.
### 🟡 Warnings
- One thing
`
	c, w := CountFindings(review)
	if c != 0 {
		t.Errorf("critical = %d", c)
	}
	if w != 1 {
		t.Errorf("warning = %d", w)
	}
}
