package toolchainguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// toolchainguard — the supply-chain gate exists, every Go version in CI comes from one place, and
// the `go` directive stays above the patch floor where this module's stdlib advisories are fixed.
//
// ⚠ THE MEASUREMENT THIS PACKAGE IS BUILT ON, AND IT CORRECTED THE ITEM THAT ASKED FOR IT.
// W6.36 filed talyvor-code as carrying EIGHT CALLED stdlib vulnerabilities, measured with
// `govulncheck ./...` on a developer machine. The gate landed and PASSED on its first CI run, and
// the job's own output said why:
//
//	Setup go version spec 1.25   ->   go version go1.25.14 linux/amd64   ->   No vulnerabilities found.
//
// `agent/go.mod` says `go 1.25` with NO patch component, so setup-go resolves the newest 1.25.x —
// go1.25.14, which carries the backports for every advisory in question. The laptop's default
// toolchain was go1.26.3, which PREDATES those same backports. **The eight were an artefact of the
// measuring machine, not a property of this repo**, and the honest conclusion was that there was
// nothing here to fix. That is recorded rather than quietly dropped.
//
// ⚠⚠ BUT THE ZERO IS LUCK, NOT A FLOOR, AND THAT IS WHAT THESE TESTS HOLD. A gate that has only
// ever been green is indistinguishable from one that cannot fail, so the floor was dropped to
// `go 1.25.0` on a throwaway commit and CI reported, in its own words:
//
//	Setup go version spec 1.25.0 -> go1.25.0 -> Your code is affected by 26 vulnerabilities
//	from the Go standard library.
//
// **One character in go.mod, twenty-six called vulnerabilities, and before the gate existed
// nothing anywhere would have said so.** talyvor-suite shipped exactly that state — its go.mod
// said `go 1.25.0` and CI built the release binary against it with 32 called vulnerabilities.
// This repo ships binaries too: the release job cross-compiles the five artifacts install.sh
// hands to customers.
//
// ⚠ WHAT IS DELIBERATELY *NOT* DONE HERE: no `toolchain` directive was added and the shipped Go
// line was not moved to 1.26. The other four repos in this estate pin a go1.26.6 floor, but each
// did so to clear vulnerabilities that were actually measured in ITS CI. Here CI measured zero, so
// raising the shipped runtime would be a change justified by a number that turned out to be an
// artefact. The invariant that IS load-bearing is pinned below instead.

// patchFloor — the highest "Fixed in: …@go1.25.N" across the advisories measured against go1.25.0
// on 2026-08-28. A `go` directive with a patch component below this ships them.
const patchFloor = 13

var (
	govulncheckInvokeRe = regexp.MustCompile(`govulncheck["']?\s+\./\.\.\.`)
	jobHeaderRe         = regexp.MustCompile(`^  ([A-Za-z_][A-Za-z0-9_-]*):\s*$`)
	goVersionLiteralRe  = regexp.MustCompile(`go-version:\s*["']?\d`)
	goDirectiveRe       = regexp.MustCompile(`(?m)^go (\d+)\.(\d+)(?:\.(\d+))?$`)
)

// ⚠ COMMENTS ARE STRIPPED BEFORE ANYTHING IS MATCHED. The word "govulncheck" is already in this
// repo's prose — in this comment and in ci.yaml's own job header — so a guard that grepped the raw
// file would stay GREEN over a ci.yaml whose vuln job had been DELETED and whose comment survived.
// ⚠ ITS ERRORS LEAN TOWARDS FALSE RED: a literal " # " inside a real command is truncated here and
// the gate reported missing. A guard that fails loudly is recoverable; one that passes on a
// comment is the defect being prevented.
func stripComment(line string) string {
	if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
		return ""
	}
	if i := strings.Index(line, " # "); i >= 0 {
		return line[:i]
	}
	return line
}

type facts struct {
	gateJob     string
	jobsSeen    int
	literalPins int // literal `go-version:` pins anywhere under jobs:
}

func readCI(src string) facts {
	var f facts
	// ⚠ ONLY KEYS UNDER `jobs:` COUNT. `on:` carries `push:` at exactly a job's indent, so a parse
	// that does not scope to the jobs block counts them — and would then report a non-zero job
	// count for a file with no jobs at all, which is the non-vacuity check lying in the
	// reassuring direction.
	inJobs, current := false, ""
	for _, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			inJobs = false
		}
		if !inJobs {
			continue
		}
		if m := jobHeaderRe.FindStringSubmatch(line); m != nil {
			current = m[1]
			f.jobsSeen++
			continue
		}
		if current == "" {
			continue
		}
		if f.gateJob == "" && govulncheckInvokeRe.MatchString(line) {
			f.gateJob = current
		}
		if goVersionLiteralRe.MatchString(line) {
			f.literalPins++
		}
	}
	return f
}

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(b)
}

func ciYAML(t *testing.T) string {
	return repoFile(t, ".github", "workflows", "ci.yaml")
}

func TestCIGatesOnGovulncheck(t *testing.T) {
	f := readCI(ciYAML(t))
	if f.jobsSeen == 0 {
		t.Fatal("parsed ZERO jobs out of ci.yaml — the parse is broken, and a broken parse cannot " +
			"see a missing gate either")
	}
	if f.gateJob == "" {
		t.Fatalf("NO job in ci.yaml runs `govulncheck ./...` (%d job(s) parsed). Nothing else in "+
			"this repo would report a vulnerable stdlib, and the release job ships five binaries.",
			f.jobsSeen)
	}
	t.Logf("MEASURED: %d job(s) in ci.yaml; %q invokes govulncheck over the module.", f.jobsSeen, f.gateJob)
}

// ⚠ ONE SOURCE OF TRUTH FOR THE GO VERSION. ci.yaml carried THREE literal `go-version: "1.25"`
// pins — the agent job, the release job, and (as added) the gate. Three numbers that can drift
// from go.mod and from each other means the job that TESTS, the job that SCANS and the job that
// SHIPS can each run a different runtime, and only one of them is the one customers get.
func TestEveryGoVersionComesFromGoMod(t *testing.T) {
	f := readCI(ciYAML(t))
	if f.literalPins != 0 {
		t.Errorf("ci.yaml carries %d literal `go-version:` pin(s). Every job must use "+
			"`go-version-file: agent/go.mod` so the runtime that is tested, the runtime that is "+
			"scanned and the runtime that is SHIPPED cannot differ.", f.literalPins)
	}
}

// ⚠ THE ONE-CHARACTER REGRESSION, PINNED. `go 1.25` resolves to the newest 1.25.x and is clean;
// `go 1.25.0` resolves to exactly go1.25.0 and CI measured 26 CALLED stdlib vulnerabilities there.
// A bare major.minor is therefore SAFE and a patch component below the floor is not.
func TestTheGoDirectiveStaysAboveThePatchFloor(t *testing.T) {
	m := goDirectiveRe.FindStringSubmatch(repoFile(t, "agent", "go.mod"))
	if m == nil {
		t.Fatal("agent/go.mod has no parseable `go X.Y[.Z]` directive — either it moved or this " +
			"parse is broken, and a broken parse reports a perfect floor")
	}
	if m[3] == "" {
		t.Logf("MEASURED: go.mod says `go %s.%s` with no patch component — setup-go resolves the "+
			"newest patch in that line, which is the state CI measured clean.", m[1], m[2])
		return
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		t.Fatalf("unparseable patch %q: %v", m[3], err)
	}
	if patch < patchFloor {
		t.Errorf("agent/go.mod pins `go %s.%s.%d`. setup-go installs that EXACT patch, and "+
			"go1.25.0 was measured in this repo's own CI at 26 CALLED stdlib vulnerabilities — in "+
			"the agent job, the vuln job, and the release job that cross-compiles the five "+
			"binaries install.sh hands to customers. Use a bare `go %s.%s`, or a patch >= %d.",
			m[1], m[2], patch, m[1], m[2], patchFloor)
	}
}

// ⚠ THE POSITIVE CONTROL, IN THE FILE RATHER THAN IN A SESSION'S SCROLLBACK. Three sessions on
// this queue shipped guards that could not fail; every one was caught by a control like this.
func TestTheseGuardsCanFail(t *testing.T) {
	real := ciYAML(t)
	if readCI(real).gateJob == "" {
		t.Fatal("the unmutated ci.yaml already has no gate — this control cannot tell a working " +
			"guard from a broken one until that is fixed")
	}

	// (1) The invocation deleted.
	var kept []string
	for _, line := range strings.Split(real, "\n") {
		if govulncheckInvokeRe.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	deleted := strings.Join(kept, "\n")
	if job := readCI(deleted).gateJob; job != "" {
		t.Errorf("MUTANT SURVIVED: invocation deleted, guard still found job %q", job)
	}

	// (2) Gate gone, prose left as a whole comment line — what a grep would pass.
	var mention []string
	for _, line := range strings.Split(real, "\n") {
		if govulncheckInvokeRe.MatchString(line) {
			mention = append(mention, "          # govulncheck ./... — removed, see PR")
			continue
		}
		mention = append(mention, line)
	}
	commented := strings.Join(mention, "\n")
	if !strings.Contains(commented, "govulncheck") {
		t.Fatal("the mention-only mutant does not mention govulncheck — the mutation is wrong, not the guard")
	}
	if job := readCI(commented).gateJob; job != "" {
		t.Errorf("MUTANT SURVIVED: only a COMMENT names govulncheck, guard reported job %q", job)
	}

	// (3) The same through the TRAILING-comment syntax — what pins stripComment's second rule.
	trailing := strings.Replace(deleted, "      - run: cd agent && go vet ./...",
		"      - run: cd agent && go vet ./... # govulncheck ./... used to run here", 1)
	if !strings.Contains(trailing, "govulncheck ./...") {
		t.Fatal("the trailing-comment mutant does not carry the mention — the mutation is wrong, not the guard")
	}
	if job := readCI(trailing).gateJob; job != "" {
		t.Errorf("MUTANT SURVIVED: govulncheck named only in a TRAILING comment, guard reported job %q", job)
	}

	// (4) `go install` without a scan.
	if job := readCI(govulncheckInvokeRe.ReplaceAllString(real, "govulncheck --help")).gateJob; job != "" {
		t.Errorf("MUTANT SURVIVED: nothing scans the module, guard reported job %q", job)
	}

	// (5) The non-vacuity counter itself: strip the whole `jobs:` block and the count must be ZERO,
	// not "however many keys happen to be indented two spaces" (`on:` has `push:` at that indent).
	var noJobs []string
	inJobs := false
	for _, line := range strings.Split(real, "\n") {
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if inJobs && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			inJobs = false
		}
		if !inJobs {
			noJobs = append(noJobs, line)
		}
	}
	if n := readCI(strings.Join(noJobs, "\n")).jobsSeen; n != 0 {
		t.Errorf("MUTANT SURVIVED: the whole `jobs:` block was removed and the parse counted %d "+
			"job(s) — the non-vacuity check is reading something that is not a job", n)
	}

	// (6) A literal pin reintroduced — a green scan with a drifting source of truth.
	literal := strings.Replace(real, "          go-version-file: agent/go.mod",
		"          go-version: \"1.25\"", 1)
	if n := readCI(literal).literalPins; n == 0 {
		t.Error("MUTANT SURVIVED: a literal `go-version:` pin was reintroduced and the guard " +
			"counted zero — it is not reading the pins it claims to read")
	}
}
