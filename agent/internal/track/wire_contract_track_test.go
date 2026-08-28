package track

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// W4.20 — THIS PACKAGE'S FAKE TRACK WAS MORE FORGIVING THAN TRACK.
//
// The pre-existing tests assert that AddComment posts `{"content":…}` — and they pass, because
// the fake server they assert against is the same test's own idea of Track: it json.Unmarshals
// into a struct the test declared, ignoring anything it does not recognise. Nothing in this
// repository had ever asked the actual server.
//
// MEASURED 2026-08-26 against a real talyvor-track (from-zero database, 26 migrations, the
// documented gateway headers in front so the request reaches a handler):
//
//	POST /v1/workspaces/ws-1/issues/{id}/comments
//	  {"content":"…","author_id":"talyvor-agent"}  -> 400 {"error":"json: unknown field \"content\""}
//	  {"body":"…","author_id":"talyvor-agent"}     -> 201, and the stored author_id came back
//	                                                  "mem-1" — the server's resolved member,
//	                                                  NOT the "talyvor-agent" that was sent
//
// Two facts, both load-bearing. Track's field is `body` (internal/model/model.go: `Body string
// \`json:"body"\``), and Track decodes with DisallowUnknownFields, so a wrong key is REJECTED
// rather than quietly dropped — AddComment could never succeed, not merely sometimes.
// And the author is always the verified session member (internal/issue/handler.go, SEC-5:
// "a supplied author_id is ignored"), so sending one has never attributed anything to the agent.
//
// trackLikeCommentServer is a fake that FAILS THE SAME WAY. Being strict is the whole point:
// a permissive fake is what let this ship.

// trackComment mirrors talyvor-track's model.Comment json tags for the fields a client may send.
// Any other key is an error here exactly as it is there.
type trackComment struct {
	IssueID  string `json:"issue_id"`
	AuthorID string `json:"author_id"`
	Body     string `json:"body"`
}

func trackLikeCommentServer(t *testing.T, got *trackComment) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields() // ← what talyvor-track's httpx.DecodeJSON does
		if err := dec.Decode(got); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"` + err.Error() + `","code":"BAD_JSON"}`))
			return
		}
		if got.Body == "" {
			// internal/issue/comments.go: "comment: IssueID, AuthorID, and Body required"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
}

func TestAddComment_BodyIsAcceptedByATrackShapedServer(t *testing.T) {
	var got trackComment
	srv := trackLikeCommentServer(t, &got)
	defer srv.Close()

	c := mustNew(t, srv.URL, "tlv_k")
	if err := c.AddComment(context.Background(), "ws-1", "iss-1", "agent task completed"); err != nil {
		t.Fatalf("AddComment against a Track-shaped server: %v\n"+
			"Track decodes comments with DisallowUnknownFields into model.Comment, whose text "+
			"field is `body`. A client that posts any other key cannot succeed.", err)
	}
	if got.Body != "agent task completed" {
		t.Fatalf("server received body = %q, want the comment text — the text must arrive in "+
			"the field Track actually reads", got.Body)
	}
}

// The fake must be able to REFUSE, or the test above proves nothing. Posting the key the client
// used to send has to fail against it, exactly as it failed against the real server.
func TestTrackLikeServer_RejectsTheKeyThatWasBeingSent(t *testing.T) {
	var got trackComment
	srv := trackLikeCommentServer(t, &got)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/workspaces/ws-1/issues/iss-1/comments",
		"application/json", bytes.NewReader([]byte(`{"content":"x","author_id":"talyvor-agent"}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a Track-shaped server accepted {\"content\":…} with %d — it is not Track-shaped, "+
			"and the test above is therefore worth nothing", resp.StatusCode)
	}
	// ⚠ THE STATUS ALONE IS NOT ENOUGH, AND A POSITIVE CONTROL IS WHAT PROVED IT. With
	// DisallowUnknownFields removed this fake STILL answered 400 — for the other reason, an
	// empty Body — so the assertion above passed while the strictness it exists to check was
	// gone. Asserting the REASON is what makes this control able to fail.
	msg, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(msg, []byte("unknown field")) {
		t.Fatalf("the 400 did not name an unknown field: %s\n"+
			"This server must refuse because the KEY is wrong, not because the decoded body "+
			"happened to be empty — otherwise a permissive fake still passes.", msg)
	}
}

// ⚠ THE PINNED-DEFECT TEST THAT STOOD HERE IS DELETED, ON ITS OWN INSTRUCTION.
// TestGetIssue_SendsTheIdentifierWhereTrackReadsAnID asserted that GetIssue spends a human key
// on Track's `/{id}` slot, documented why (Track had no identifier route), and closed with:
// "Anyone making Track resolve identifiers should delete this test and the note above it."
// talyvor-track did — `GET /v1/workspaces/{wsID}/issues/by-identifier/{identifier}`, whose own
// handler comment names this client as the reason. The return trip never happened until now.
// The behaviour it pinned is replaced, not dropped: by_identifier_fallback_test.go covers the
// id-first order, the fallback, the credential on both requests, the genuine miss and the 500.
