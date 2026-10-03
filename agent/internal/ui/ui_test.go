package ui

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"
)

// everything renders every helper once, so a test can ask whether ANY of them emitted a code.
func everything(s Style) string {
	return strings.Join([]string{
		s.Eyebrow("issue"), s.Issue("ENG-42"), s.Figure("1234"), s.Muted("from branch"),
		s.OK("reachable"), s.Warn("not found"), s.Fail("rejected"), s.ErrorPrefix(),
		s.Row("lens", "x"), s.Row("", "y"), s.Sep(),
	}, "\n")
}

// Piped output and log files get the words, never the escape codes.
func TestANonTerminalWriterGetsPlainText(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	got := everything(For(&bytes.Buffer{}))
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("a bytes.Buffer is not a terminal, but got escape codes:\n%q", got)
	}
	if !strings.Contains(got, "ISSUE") || !strings.Contains(got, "✓ reachable") {
		t.Fatalf("plain output lost its words:\n%s", got)
	}
}

// NO_COLOR and TERM=dumb switch colour off on a character device; with neither set it is on.
// /dev/null stands in for a terminal: it is the character device every Unix CI runner has.
func TestNoColorAndDumbTerminalsAreRespected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/null")
	}
	dev, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	for _, tc := range []struct {
		noColor, term string
		want          bool
	}{
		{"", "xterm-256color", true}, // the control: without it, an always-off For would pass
		{"1", "xterm-256color", false},
		{"", "dumb", false},
	} {
		t.Setenv("NO_COLOR", tc.noColor)
		t.Setenv("TERM", tc.term)
		if got := For(dev).Color(); got != tc.want {
			t.Errorf("NO_COLOR=%q TERM=%q: colour = %v, want %v", tc.noColor, tc.term, got, tc.want)
		}
	}
}

// The grammar: the eyebrow is uppercase and faint, and the one accent belongs to the issue.
func TestTheGrammar(t *testing.T) {
	c := Colored()
	if got := c.Eyebrow("issue"); got != "\x1b[2mISSUE\x1b[0m" {
		t.Errorf("eyebrow = %q", got)
	}
	if got := c.Issue("ENG-42"); got != "\x1b[1;36mENG-42\x1b[0m" {
		t.Errorf("issue = %q", got)
	}
	// Rows align: the content of every row starts at the same column, label or not.
	a, b := Plain().Row("anthropic", "x"), Plain().Row("", "y")
	if strings.Index(a, "x") != strings.Index(b, "y") {
		t.Errorf("rows do not align:\n%q\n%q", a, b)
	}
}
