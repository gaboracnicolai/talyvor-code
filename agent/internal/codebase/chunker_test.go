package codebase

import (
	"strings"
	"testing"
)

// TestChunkFile_Go_SplitsByDeclaration — a Go file is chunked at top-level
// declaration boundaries (func/type), each decl carrying its preceding doc
// comment, with the package+import header as its own chunk. Function-aware where
// cheap (the recon's "block-aware where cheap"), so a retrieved chunk maps to a
// coherent unit, not an arbitrary line window.
func TestChunkFile_Go_SplitsByDeclaration(t *testing.T) {
	src := `package auth

import "errors"

// Login validates credentials and returns a session token.
func Login(user, pass string) (string, error) {
	if user == "" {
		return "", errors.New("empty user")
	}
	return "tok-" + user, nil
}

// Session holds a logged-in user's state.
type Session struct {
	User  string
	Token string
}

func Logout(token string) error {
	return nil
}
`
	chunks := ChunkFile("internal/auth/auth.go", src)
	if len(chunks) < 3 {
		t.Fatalf("expected ≥3 chunks (header + Login + Session + Logout), got %d", len(chunks))
	}
	// Every chunk carries file + language + a 1-based line span that round-trips.
	for _, c := range chunks {
		if c.File != "internal/auth/auth.go" || c.Language != "Go" {
			t.Errorf("chunk metadata wrong: %+v", c)
		}
		if c.StartLine < 1 || c.EndLine < c.StartLine {
			t.Errorf("bad line span: %+v", c)
		}
	}
	// Login's chunk includes its doc comment AND its body — a coherent unit.
	login := chunkContaining(chunks, "func Login")
	if login == nil {
		t.Fatal("no chunk contains func Login")
	}
	if !strings.Contains(login.Content, "Login validates credentials") {
		t.Error("Login chunk must include its preceding doc comment")
	}
	if !strings.Contains(login.Content, `errors.New("empty user")`) {
		t.Error("Login chunk must include the function body")
	}
	// Login and Logout are DISTINCT chunks (declaration boundaries respected).
	logout := chunkContaining(chunks, "func Logout")
	if logout == nil || logout.StartLine == login.StartLine {
		t.Error("Login and Logout must be separate chunks")
	}
}

// TestChunkFile_Fallback_LineWindows — a non-code file (no decls) falls back to
// overlapping line windows that fully cover the file.
func TestChunkFile_Fallback_LineWindows(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 130; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 3))
		b.WriteByte('\n')
	}
	chunks := ChunkFile("docs/notes.md", b.String())
	if len(chunks) < 2 {
		t.Fatalf("130-line file should produce multiple windows, got %d", len(chunks))
	}
	// Windows cover the file start-to-end with no gap (consecutive windows overlap
	// or abut — the last window reaches the final line).
	if chunks[0].StartLine != 1 {
		t.Errorf("first window must start at line 1, got %d", chunks[0].StartLine)
	}
	last := chunks[len(chunks)-1]
	if last.EndLine != 130 {
		t.Errorf("last window must reach line 130, got %d", last.EndLine)
	}
	// Overlap: each window (after the first) starts no later than the previous end+1.
	for i := 1; i < len(chunks); i++ {
		if chunks[i].StartLine > chunks[i-1].EndLine+1 {
			t.Errorf("gap between window %d (end %d) and %d (start %d)", i-1, chunks[i-1].EndLine, i, chunks[i].StartLine)
		}
	}
}

// TestChunkFile_Empty — empty / whitespace-only content yields no chunks (nothing
// to embed).
func TestChunkFile_Empty(t *testing.T) {
	if got := ChunkFile("x.go", ""); len(got) != 0 {
		t.Errorf("empty file must yield 0 chunks, got %d", len(got))
	}
	if got := ChunkFile("x.txt", "   \n\n  \n"); len(got) != 0 {
		t.Errorf("whitespace-only file must yield 0 chunks, got %d", len(got))
	}
}

func chunkContaining(chunks []Chunk, needle string) *Chunk {
	for i := range chunks {
		if strings.Contains(chunks[i].Content, needle) {
			return &chunks[i]
		}
	}
	return nil
}
