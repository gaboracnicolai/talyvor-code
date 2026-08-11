package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The JetBrains Gradle wrapper is COMMITTED (gradlew, gradlew.bat, gradle-wrapper.jar and
// gradle-wrapper.properties are all tracked, and .gitignore says so in as many words). A committed
// wrapper exists for exactly one reason: so a reader with no system Gradle can build. Three
// documents told that reader to run `gradle wrapper` first — which fails outright without a system
// Gradle (`gradle: command not found`, exit 127), and, for a reader who has one, REWRITES the
// pinned distributionUrl to whatever version happens to be installed. Measured with the repo's own
// files: a local Gradle 9.5.1 regenerated the wrapper at `gradle-9.5.1-bin.zip` over the pinned 8.7,
// silently moving the developer's build off the version CI provisions.
//
// This test is about COMMANDS, not prose. It reads the shell blocks a reader would paste and fails
// if any of them invokes a system `gradle` while the wrapper is committed. A document that merely
// *describes* the wrapper wrongly in a sentence is not visible to it — that limit is deliberate and
// stated rather than papered over with a brittle phrase match.

var (
	fenceOpen  = regexp.MustCompile("^\\s*```\\s*(bash|sh|shell|console)\\s*$")
	fenceClose = regexp.MustCompile("^\\s*```\\s*$")
	// The pinned Gradle version, read out of the wrapper's own properties file rather than
	// written down here — a guard that carries its own copy of the number it is checking can
	// agree with itself while the file moves.
	distURLVersion = regexp.MustCompile(`gradle-([0-9][0-9A-Za-z.\-]*)-(?:bin|all)\.zip`)
)

// docDirsToSkip are directories whose Markdown is not this repository's documentation:
// dependency trees, build output, and (below) any nested checkout.
var docDirsToSkip = map[string]bool{
	"node_modules": true, "build": true, "out": true, "dist": true,
	".gradle": true, ".intellijPlatform": true, ".remember": true, "testdata": true,
}

// gradleCall is one bare-`gradle` invocation found in a fenced shell block.
type gradleCall struct {
	file string
	line int
	text string
}

// scanShellBlocks walks the fenced bash/sh blocks of a Markdown source and returns every command
// whose command word is exactly `gradle`, plus a count of every `./gradlew` invocation it saw. The
// gradlew count is what makes a zero meaningful: an extractor that reads nothing also returns zero
// violations, and the two are indistinguishable without it.
func scanShellBlocks(path string, src []byte) (bad []gradleCall, gradlewSeen int) {
	sc := bufio.NewScanner(strings.NewReader(string(src)))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	inBlock, lineNo := false, 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !inBlock {
			if fenceOpen.MatchString(line) {
				inBlock = true
			}
			continue
		}
		if fenceClose.MatchString(line) {
			inBlock = false
			continue
		}
		// Strip a trailing comment, then split the line into the command segments a shell
		// would run so `cd jetbrains && gradle wrapper` is seen as two commands, not one.
		code := line
		if i := strings.Index(code, "#"); i >= 0 {
			code = code[:i]
		}
		for _, seg := range strings.FieldsFunc(code, func(r rune) bool {
			return r == ';' || r == '|' || r == '&'
		}) {
			fields := strings.Fields(seg)
			if len(fields) == 0 {
				continue
			}
			// Skip leading environment assignments (FOO=bar gradle …).
			w := 0
			for w < len(fields) && strings.Contains(fields[w], "=") && !strings.HasPrefix(fields[w], "-") {
				w++
			}
			if w >= len(fields) {
				continue
			}
			switch cmd := fields[w]; cmd {
			case "gradle":
				bad = append(bad, gradleCall{file: path, line: lineNo, text: strings.TrimSpace(line)})
			case "./gradlew", "gradlew.bat", ".\\gradlew.bat":
				gradlewSeen++
			}
		}
	}
	return bad, gradlewSeen
}

// repoRoot walks up from the test's working directory to the directory that carries the CI
// workflow. Anchoring on a file that must exist means a moved test package cannot silently start
// scanning nothing.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "ci.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find the repository root (no .github/workflows/ci.yaml at or above %q) — "+
		"this guard scans repository documentation and must never pass by scanning nothing", dir)
	return ""
}

// TestShellBlockScannerCanSeeABareGradleCall proves the instrument works before any zero it
// produces is believed. Both directions: a bare `gradle` is caught, `./gradlew` is not, and text
// outside a fenced block is invisible.
func TestShellBlockScannerCanSeeABareGradleCall(t *testing.T) {
	const fixture = "Prose mentioning gradle wrapper, which is not a command.\n" +
		"\n" +
		"```bash\n" +
		"cd jetbrains\n" +
		"gradle wrapper        # one-time\n" +
		"./gradlew buildPlugin\n" +
		"```\n" +
		"\n" +
		"```text\n" +
		"gradle wrapper\n" +
		"```\n"

	bad, gradlew := scanShellBlocks("fixture.md", []byte(fixture))
	if len(bad) != 1 {
		t.Fatalf("scanner found %d bare-gradle calls in the fixture, want exactly 1: %+v", len(bad), bad)
	}
	if bad[0].line != 5 {
		t.Errorf("bare-gradle call reported at line %d, want 5", bad[0].line)
	}
	if gradlew != 1 {
		t.Errorf("scanner counted %d ./gradlew calls in the fixture, want 1", gradlew)
	}

	// The inverse: a document that only uses the wrapper must produce no finding, or the rule
	// would be a catch-all that the fix below could never satisfy.
	clean := "```bash\ncd jetbrains\n./gradlew test buildPlugin\n```\n"
	if bad, gradlew := scanShellBlocks("clean.md", []byte(clean)); len(bad) != 0 || gradlew != 1 {
		t.Errorf("clean fixture: %d violations / %d gradlew calls, want 0 / 1", len(bad), gradlew)
	}
}

// TestTheGradleWrapperIsStillCommitted is this rule's PREMISE, asserted rather than assumed. If the
// wrapper stops being tracked, "documentation must not tell you to generate it" is no longer the
// right rule — and the failure that follows is the point: the rule gets deleted deliberately
// instead of going quiet and reading as coverage.
func TestTheGradleWrapperIsStillCommitted(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"jetbrains/gradlew",
		"jetbrains/gradlew.bat",
		"jetbrains/gradle/wrapper/gradle-wrapper.jar",
		"jetbrains/gradle/wrapper/gradle-wrapper.properties",
	} {
		st, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s is missing: %v\n"+
				"The committed wrapper is the premise of TestDocsDoNotTellTheReaderToGenerateACommittedWrapper. "+
				"If the wrapper is deliberately no longer shipped, delete that rule in the same change.", rel, err)
		}
		if st.Size() == 0 {
			t.Fatalf("%s is empty — a placeholder is not a committed wrapper", rel)
		}
	}

	props, err := os.ReadFile(filepath.Join(root, "jetbrains/gradle/wrapper/gradle-wrapper.properties"))
	if err != nil {
		t.Fatalf("read gradle-wrapper.properties: %v", err)
	}
	m := distURLVersion.FindSubmatch(props)
	if m == nil {
		t.Fatalf("gradle-wrapper.properties pins no recognisable distribution:\n%s", props)
	}
	t.Logf("wrapper pins Gradle %s — this is the version `gradle wrapper` would overwrite", m[1])
}

// TestDocsDoNotTellTheReaderToGenerateACommittedWrapper is the rule.
func TestDocsDoNotTellTheReaderToGenerateACommittedWrapper(t *testing.T) {
	root := repoRoot(t)

	var (
		violations       []gradleCall
		gradlewInWrapper int
		scanned          []string
		scannedSet       = map[string]bool{}
	)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root {
				if strings.HasPrefix(name, ".git") || docDirsToSkip[name] {
					return filepath.SkipDir
				}
				// A directory carrying its own .git is a different repository that
				// happens to sit in the tree. Its documentation is not ours.
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		bad, gw := scanShellBlocks(rel, src)
		violations = append(violations, bad...)
		if strings.HasPrefix(rel, "jetbrains/") {
			gradlewInWrapper += gw
		}
		scanned = append(scanned, rel)
		scannedSet[rel] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	// Three floors before the zero means anything.
	//
	// ⚠ THE SECOND ONE IS HERE BECAUSE A CONTROL CAUGHT ITS ABSENCE. The first version counted
	// `./gradlew` invocations repo-wide, and adding "jetbrains" to the skip map above — the exact
	// way this scan's input set can silently shrink — left the guard GREEN, because the ROOT
	// README's own `./gradlew` line kept the count above zero. A floor satisfied by a different
	// document than the one whose absence matters is not a floor. It is scoped to the documents
	// that describe the wrapper, and the required-file check below is computed by a SECOND,
	// INDEPENDENT enumeration that does not consult the skip map at all.
	if len(scanned) == 0 {
		t.Fatalf("scanned no Markdown at all under %s — the walk is broken, not the repository clean", root)
	}
	for _, dir := range []string{".", "jetbrains"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("independent enumeration of %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
				continue
			}
			want := filepath.ToSlash(filepath.Join(dir, e.Name()))
			want = strings.TrimPrefix(want, "./")
			if !scannedSet[want] {
				t.Fatalf("%s exists but the walk never scanned it (scanned: %v). "+
					"A document this rule is meant to cover fell out of the walk's input set — "+
					"the zero below would be a fact about the walk, not about the repository.", want, scanned)
			}
		}
	}
	if gradlewInWrapper == 0 {
		t.Fatalf("scanned %d Markdown files and found no `./gradlew` invocation under jetbrains/. "+
			"The documented JetBrains build uses the committed wrapper, so a zero here means this "+
			"scan never read those blocks — not that they are clean. Files: %v", len(scanned), scanned)
	}

	if len(violations) > 0 {
		for _, v := range violations {
			t.Errorf("%s:%d invokes a system `gradle` while the wrapper is committed:\n    %s\n"+
				"    Use ./gradlew — a reader without a system Gradle gets `gradle: command not found` "+
				"(exit 127), and a reader with one rewrites the pinned distributionUrl to their own version.",
				v.file, v.line, v.text)
		}
		t.Fatalf("%d bare-`gradle` invocation(s) across %d scanned documents", len(violations), len(scanned))
	}
	t.Logf("scanned %d Markdown files (%v), %d ./gradlew invocations under jetbrains/, 0 bare-gradle invocations", len(scanned), scanned, gradlewInWrapper)
}
