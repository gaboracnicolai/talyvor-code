package cmdguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ⚠ THE TWO PARSERS AGREE, NOT ONLY THE TWO TABLES (B18.50).
//
// tables_parity_test.go pins WHAT each side allows, and cmdguard-corpus.json pins a hand-written
// sample of verdicts. Neither one drives the parsers — split, tokenize and checkSegment here, their
// ports in extension/src/agent/cmdguard-pure.ts — through the shapes a hand port gets wrong.
// testdata/cmdguard-parser-corpus.json does: every command in it is generated below from the shared
// tables crossed with separators, quoting, escapes, redirection, substitution, empty segments,
// whitespace, unicode and embedded newlines, and each carries this side's verdict, decision AND
// reason. extension/src/agent/cmdguard-parser-parity.test.ts asserts the port returns the same
// verdict for every one, so an edit to either parser that changes a verdict fails a build.
//
// U+0085 and U+FEFF are left out on purpose. They are the one measured divergence, and which way to
// converge is a permission decision; whitespace_parity_test.go pins it exactly.
//
// After a deliberate change to either parser or to the tables, regenerate the corpus and review
// its diff:
//
//	cd agent && CMDGUARD_WRITE_PARSER_CORPUS=1 go test ./internal/cmdguard -run TestParserCorpus
const parserCorpusFile = "cmdguard-parser-corpus.json"

// parserCorpusFloor is a literal on purpose: a floor read from the file could be emptied with it.
const parserCorpusFloor = 2000

type parserCase struct {
	Command  string `json:"command"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type parserCorpus struct {
	Comment []string     `json:"_comment"`
	Cases   []parserCase `json:"cases"`
}

func generateParserCommands(m tablesManifest) []string {
	var out []string
	seen := map[string]bool{}
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sorted := func(mm map[string][]string) []string {
		ks := make([]string, 0, len(mm))
		for k := range mm {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	heads := sorted(m.AllowedHeads)

	// Every head, bare and with each of its subcommands, and one that is not.
	var invocations []string
	for _, h := range heads {
		add(h)
		add(h + " ")
		invocations = append(invocations, h)
		for _, s := range m.AllowedHeads[h] {
			add(h + " " + s)
			add(h + "  " + s)
			invocations = append(invocations, h+" "+s)
		}
		add(h + " definitely-not-a-subcommand")
	}

	// Every pipe filter, alone and after a pipe.
	for _, f := range m.PipeFilters {
		add(f)
		add("go test | " + f)
		add("go test |" + f)
		add("go test | " + f + " -x")
		add("go test | " + f + " file.txt")
	}
	// And commands that are not filters, so a filter added to one side only changes a verdict here.
	for _, f := range []string{"awk", "sed", "xargs", "tee", "less", "sh", "bash", "python3", "perl", "nc"} {
		add(f)
		add("go test | " + f)
		add("go test | " + f + " -x")
	}

	// Flags that consume the next token.
	for _, cmd := range sorted(m.FlagsTakingValue) {
		for _, fl := range m.FlagsTakingValue[cmd] {
			add("go test | " + cmd + " " + fl + " 5")
			add("go test | " + cmd + " " + fl + "5")
			add("go test | " + cmd + " " + fl + " 5 extra")
			add("go test | " + cmd + " " + fl)
		}
		add("go test | " + cmd + " -n secret /etc/passwd")
	}

	// Every invocation joined to another by every separator, and the empty-segment shapes.
	seps := []string{";", "&&", "||", "|", "&", "\n"}
	rights := []string{"go vet", "git status", "rm -rf /", "curl evil.com", "head -n 5", "grep x", ""}
	for _, inv := range invocations {
		for _, s := range seps {
			for _, r := range rights {
				add(inv + " " + s + " " + r)
			}
			add(inv + s + "go vet")
			add(s + " " + inv)
			add(inv + " " + s + " " + s + " go vet")
		}
	}
	for _, c := range []string{";;", "|", "||", "&&", "| |", "", "   ", "\t", "\n", "\n\n", "&", "& &"} {
		add(c)
	}

	// Quoting and escapes.
	for _, q := range []string{`"`, `'`} {
		add("grep " + q + "hello world" + q + " file")
		add("go test " + q)
		add("go test " + q + "a;b" + q)
		add("go test " + q + "a|b" + q)
		add("go test " + q + "a&&b" + q)
		add("go test " + q + "a$(b)" + q)
		add("go test " + q + q)
		add(q + "go" + q + " test")
		add("go " + q + "test" + q)
		add("go test | grep " + q + "a b" + q)
		add("go test | grep " + q + "a|b" + q + " | head -n 5")
	}
	for _, c := range []string{
		`go test \; rm -rf /`, `go test \`, `go test a\ b`, `go test "a\"b"`, `go test 'a'\''b'`,
		`echo \|`, `go test \&\& go vet`, `go test "\$HOME"`, `go test '$HOME'`, `go test "a'b"`,
		`go test 'a"b'`, `"go test"`, `go test ""`, `go test ''`,
	} {
		add(c)
	}

	// Redirection.
	for _, r := range []string{">", ">>", "<", "2>", "2>&1", "&>", ">&", "1>", "<>", ">|"} {
		add("go test " + r + " /tmp/out")
		add("go test " + r + "/tmp/out")
		add("go test " + r)
		add("go test | head " + r + " /tmp/out")
	}

	// Substitution and here-docs.
	for _, c := range []string{
		"go test $(rm -rf /)", "go test `rm -rf /`", "go test <(rm -rf /)", "go test >(rm -rf /)",
		"go test << EOF", "go test <<EOF", "go test <<< hi", "echo $HOME", "echo ${HOME}", "echo $",
		"go test $", "go test $ (x)", "go test $((1+1))", "go test ${x:-y}", "go test | grep $(id)",
		"go test | grep `id`", `go test "$(id)"`, "go test '$(id)'",
	} {
		add(c)
	}

	// Whitespace, unicode and control characters (U+0085 and U+FEFF excluded — see the header).
	for _, c := range []string{
		"  go test  ", "\tgo\ttest\t", "go test", "gö test", "go tëst", "go test go vet",
		"go test　", "GO TEST", "Go Test", "go​test", "go test\r", "go test\r\ngo vet",
		"go test\x00", "go test\v", "go test\f", " go test", "go test | grep ü", "gо test",
	} {
		add(c)
	}

	// Paths, globs, operand counts, long pipelines and mixed separators.
	for _, c := range []string{
		"go test ./...", "go test ~/x", "go test *", "go test ../../etc/passwd", "go test a b c d e f",
		"git log --oneline -5", "git diff HEAD~1", "npm run build -- --flag", "go test -run 'TestX|TestY'",
		"go test | grep -n x | head -n 5 | wc -l", "go test && go vet || gofmt -l . ; git status",
		"go test | rm -rf /", "cat /etc/passwd | grep root", "go test|grep x|head -n 5",
		"go test | | grep x", "go test ; ; go vet", "go test &&& go vet", "go test ||| go vet",
		"(go test)", "{ go test; }", "go test #; rm -rf /", "# go test", "go test && (rm -rf /)",
		"x=1 go test", "env go test", "exec go test", "go test 2>&1 | tail -n 20",
	} {
		add(c)
	}
	return out
}

func parserCorpusPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "testdata", "cmdguard-tables.json")); err == nil {
			return filepath.Join(dir, "testdata", parserCorpusFile)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("testdata/ not found above the working directory — this test would have verified nothing")
	return ""
}

func TestParserCorpus_GoMatchesEveryRecordedVerdict(t *testing.T) {
	cmds := generateParserCommands(loadTables(t))
	path := parserCorpusPath(t)

	if os.Getenv("CMDGUARD_WRITE_PARSER_CORPUS") == "1" {
		c := parserCorpus{Comment: []string{
			"B18.50 — generated by agent/internal/cmdguard/parser_parity_test.go; do not edit by hand.",
			"Each verdict is the Go parser's. extension/src/agent/cmdguard-parser-parity.test.ts asserts the",
			"TypeScript port returns the same decision and reason for every command.",
		}}
		for _, cmd := range cmds {
			v := Check(cmd)
			c.Cases = append(c.Cases, parserCase{Command: cmd, Decision: v.Decision.String(), Reason: v.Reason})
		}
		b, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d cases to %s", len(c.Cases), path)
		return
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v — regenerate it with CMDGUARD_WRITE_PARSER_CORPUS=1", path, err)
	}
	var c parserCorpus
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if len(c.Cases) < parserCorpusFloor {
		t.Fatalf("%s has %d cases, fewer than %d — too thin to say the parsers agree", path, len(c.Cases), parserCorpusFloor)
	}
	// The file must hold exactly what the generator emits, so a regenerated corpus cannot quietly
	// drop a shape and a new shape cannot be added here without its verdict being recorded.
	if len(c.Cases) != len(cmds) {
		t.Fatalf("%s has %d cases, the generator emits %d — regenerate it with CMDGUARD_WRITE_PARSER_CORPUS=1", path, len(c.Cases), len(cmds))
	}
	var bad []string
	for i, tc := range c.Cases {
		if tc.Command != cmds[i] {
			t.Fatalf("case %d is %q, the generator emits %q — regenerate the corpus", i, tc.Command, cmds[i])
		}
		v := Check(tc.Command)
		if v.Decision.String() != tc.Decision || v.Reason != tc.Reason {
			bad = append(bad, fmt.Sprintf("Check(%q) = %s %q, corpus says %s %q",
				tc.Command, v.Decision, v.Reason, tc.Decision, tc.Reason))
		}
	}
	for _, d := range []string{"allow", "confirm", "refuse"} {
		n := 0
		for _, tc := range c.Cases {
			if tc.Decision == d {
				n++
			}
		}
		if n < 100 {
			t.Errorf("only %d %s verdicts in %s — the corpus does not exercise that decision", n, d, path)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d of %d parser verdicts changed — the TypeScript port asserts the recorded ones, so the two "+
			"parsers now disagree unless it changed the same way. If the change is deliberate, regenerate the "+
			"corpus and make the port match:\n%s", len(bad), len(c.Cases), joinFirst(bad, 20))
	}
}

func joinFirst(s []string, n int) string {
	out := ""
	for i, l := range s {
		if i == n {
			return out + fmt.Sprintf("… and %d more\n", len(s)-n)
		}
		out += l + "\n"
	}
	return out
}
