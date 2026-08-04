// Package cmdguard decides whether a model-authored shell command may run unattended.
//
// ⚠ WHAT IT IS PROTECTING. The `run` tool hands a model-authored string to `sh -c` with a cwd and a
// timeout and nothing else. That shell reaches ~/.ssh, the environment, and the network. The
// --iterative flag does not help: it gates whether the agent runs at all, it is set once, and `run`
// executes many times inside that one decision.
//
// ⚠ WHY AN ALLOWLIST AND NOT A DENYLIST. The harmful space is unbounded and `sh -c` composes, so a
// denylist is a list of the attacks someone already thought of. `curl`, `nc`, `python -c`, `>`
// into a dotfile, a base64'd payload — each needs its own entry, and the first one nobody wrote
// down is the one that runs. Default-deny inverts that: the failure mode of a missing entry is a
// confirmation prompt, not an execution.
//
// ⚠ WHY IT PARSES INSTEAD OF PREFIX-MATCHING. `go test; curl evil.com` and `go test && rm -rf ~`
// both begin with an allowed verb. Any check that looks at the start of the string says yes to
// both. This splits the command into its actual segments first and judges every one.
//
// ⚠ AND A COMMAND IT CANNOT PARSE IS REFUSED, never confirmed and never run. Command substitution
// hides the real command behind another shell: `$(...)` and backticks can produce anything at run
// time, so no confirmation prompt could show a user what they are agreeing to. Showing them
// `go test $(cat /tmp/x)` and calling that informed consent would be worse than refusing.
package cmdguard

import (
	"fmt"
	"strings"
)

// Decision is what the caller must do with a command.
type Decision int

const (
	// Allow — every segment is a known build/test/lint/vcs-read command. Runs unattended.
	Allow Decision = iota
	// Confirm — parsed cleanly, but something in it is not on the allowlist. Requires ONE
	// confirmation that shows the exact command.
	Confirm
	// Refuse — cannot be parsed into segments whose effect is knowable in advance. Never runs.
	Refuse
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Confirm:
		return "confirm"
	default:
		return "refuse"
	}
}

// Verdict carries the decision and the reason a human needs to act on it.
type Verdict struct {
	Decision Decision
	// Reason names the SEGMENT that forced the decision, so a confirmation prompt can say what is
	// unusual rather than showing the whole command and asking the user to spot it.
	Reason string
}

// allowedHeads maps a command to the subcommands that are read-only or build-shaped.
// An empty set means the command is allowed with any arguments.
//
// ⚠ vcs is READ-ONLY on purpose. `git status/diff/log` are how an agent orients; `git push`,
// `git commit`, `git checkout` and `git clean` change or destroy work and go through confirmation.
var allowedHeads = map[string]map[string]bool{
	// build / test / lint toolchains — the work the agent is for.
	"go":            {"build": true, "test": true, "vet": true, "fmt": true, "list": true, "mod": true},
	"gofmt":         {},
	"golangci-lint": {},
	"cargo":         {"build": true, "test": true, "check": true, "clippy": true, "fmt": true},
	"mvn":           {},
	"gradle":        {},
	"make":          {},
	"tsc":           {},
	"eslint":        {},
	"prettier":      {},
	"ruff":          {},
	"pytest":        {},
	"jest":          {},
	"vitest":        {},
	"npm":           {"test": true, "run": true, "ci": true, "ls": true},
	"pnpm":          {"test": true, "run": true, "build": true, "lint": true, "typecheck": true, "install": true},
	"yarn":          {"test": true, "run": true, "build": true, "lint": true},
	// vcs, READ paths only.
	"git": {
		"status": true, "diff": true, "log": true, "show": true, "branch": true,
		"rev-parse": true, "ls-files": true, "describe": true, "blame": true, "remote": true,
	},
}

// pipeFilters may appear AFTER a pipe. They read stdin, and the check below refuses them a file
// operand — `go test | grep FAIL` is ordinary; `grep secret ~/.ssh/id_rsa` is not, and the only
// difference is an operand.
var pipeFilters = map[string]bool{
	"head": true, "tail": true, "wc": true, "sort": true, "uniq": true,
	"grep": true, "cut": true, "tr": true, "jq": true,
}

// flagsTakingValue are the flags that consume the NEXT token, PER COMMAND — `-n` takes a value for
// `head` and takes none for `grep`. A single shared set got this wrong in the permissive direction:
// `grep -n secret ~/.ssh/id_rsa` had its file counted as `-n`'s value, so the operand check saw one
// operand and allowed a read of the key. Wrong the other way costs only a confirmation.
var flagsTakingValue = map[string]map[string]bool{
	"grep": {"-e": true, "--regexp": true, "-m": true, "--max-count": true},
	"head": {"-n": true, "-c": true},
	"tail": {"-n": true, "-c": true},
	"cut":  {"-d": true, "-f": true},
	"sort": {"-k": true, "-t": true},
	"jq":   {},
}

// Check classifies a model-authored command.
func Check(command string) Verdict {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return Verdict{Refuse, "empty command"}
	}

	// ⚠ SUBSTITUTION IS UNPARSEABLE BY CONSTRUCTION. What `$(...)` expands to is not known until a
	// shell runs it, so neither this check nor a human reading a prompt can know what would run.
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return Verdict{Refuse, "command substitution ($(…) or backticks) — what it expands to is not knowable before it runs"}
	}
	// Process substitution and here-documents have the same problem.
	if strings.Contains(cmd, "<(") || strings.Contains(cmd, ">(") || strings.Contains(cmd, "<<") {
		return Verdict{Refuse, "process substitution or here-document — the effective command is not knowable in advance"}
	}

	segments, err := split(cmd)
	if err != nil {
		return Verdict{Refuse, err.Error()}
	}

	for _, seg := range segments {
		if v := checkSegment(seg); v.Decision != Allow {
			return v
		}
	}
	return Verdict{Allow, ""}
}

// segment is one simple command plus whether it followed a pipe.
type segment struct {
	tokens    []string
	afterPipe bool
}

// split breaks a command on the shell operators that start a NEW command, honouring quotes so a
// separator inside a string is not treated as one. It deliberately understands a small grammar and
// refuses anything outside it, because the alternative — guessing — is how a prefix match fails.
func split(cmd string) ([]segment, error) {
	var (
		segs      []segment
		cur       []rune
		afterPipe bool
		quote     rune
	)
	flush := func(nextAfterPipe bool) error {
		text := strings.TrimSpace(string(cur))
		cur = nil
		if text != "" {
			toks, err := tokenize(text)
			if err != nil {
				return err
			}
			if len(toks) > 0 {
				segs = append(segs, segment{tokens: toks, afterPipe: afterPipe})
			}
		}
		afterPipe = nextAfterPipe
		return nil
	}

	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			cur = append(cur, c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur = append(cur, c)
		case '\\':
			// A line continuation or escaped separator changes what the shell sees; rather than
			// model every escape, refuse and let the user confirm explicitly.
			return nil, fmt.Errorf("backslash escaping — refused rather than guessed")
		case ';', '\n':
			if err := flush(false); err != nil {
				return nil, err
			}
		case '&':
			if i+1 < len(rs) && rs[i+1] == '&' {
				i++
				if err := flush(false); err != nil {
					return nil, err
				}
			} else {
				// A lone & backgrounds the command; it then outlives the timeout that is the only
				// bound this tool had.
				return nil, fmt.Errorf("backgrounding (&) — the process would outlive the run timeout")
			}
		case '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				i++
				if err := flush(false); err != nil {
					return nil, err
				}
			} else {
				if err := flush(true); err != nil {
					return nil, err
				}
			}
		default:
			cur = append(cur, c)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced quote — the command cannot be parsed")
	}
	if err := flush(false); err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("no command found")
	}
	return segs, nil
}

// tokenize splits one simple command into tokens, stripping surrounding quotes.
func tokenize(s string) ([]string, error) {
	var (
		toks  []string
		cur   []rune
		quote rune
	)
	push := func() {
		if len(cur) > 0 {
			toks = append(toks, string(cur))
			cur = nil
		}
	}
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur = append(cur, c)
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t':
			push()
		default:
			cur = append(cur, c)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced quote — the command cannot be parsed")
	}
	push()
	return toks, nil
}

func checkSegment(seg segment) Verdict {
	if len(seg.tokens) == 0 {
		return Verdict{Refuse, "empty segment"}
	}
	head := seg.tokens[0]

	// ⚠ A REDIRECT WRITES OUTSIDE THE COMMAND'S OWN OUTPUT. `go test > /tmp/x` is ordinary and
	// `printf ... > ~/.ssh/authorized_keys` is not, and both are parseable — so this is a
	// confirmation, not a refusal: the user is shown exactly where it writes.
	for _, t := range seg.tokens {
		if t == ">" || t == ">>" || t == "<" || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "<") {
			return Verdict{Confirm, fmt.Sprintf("%s redirects output (%s)", head, t)}
		}
	}

	// An env assignment prefix (FOO=bar cmd) hides the real head.
	if strings.Contains(head, "=") && !strings.HasPrefix(head, "-") {
		return Verdict{Confirm, fmt.Sprintf("environment assignment before the command (%s)", head)}
	}
	// A path-qualified command is not the tool of the same name.
	if strings.ContainsAny(head, "/\\") {
		return Verdict{Confirm, fmt.Sprintf("runs a program by path (%s), not a known toolchain command", head)}
	}

	if seg.afterPipe {
		if !pipeFilters[head] {
			return Verdict{Confirm, fmt.Sprintf("%s is not a read-only filter", head)}
		}
		// ⚠ A FILTER WITH A FILE OPERAND IS NOT READING THE PIPE. `grep x file` opens the file
		// regardless of stdin, so the operand count is what separates the two.
		if operands(seg.tokens[1:], head) > allowedOperands(head) {
			return Verdict{Confirm, fmt.Sprintf("%s is given a file to read, not just the piped input", head)}
		}
		return Verdict{Allow, ""}
	}

	subs, ok := allowedHeads[head]
	if !ok {
		return Verdict{Confirm, fmt.Sprintf("%s is not a build, test, lint or version-control read command", head)}
	}
	if len(subs) == 0 {
		return Verdict{Allow, ""}
	}
	// ⚠ LOOK FOR AN ALLOWED SUBCOMMAND, DO NOT GUESS BY POSITION. `pnpm --filter web test` puts the
	// flag's VALUE where a positional scan expects the subcommand, and reading "web" there refused
	// an ordinary monorepo command. Scanning for a member of the allowlist is both more permissive
	// on honest input and no weaker on hostile input: the head's own allowlist still bounds it, and
	// anything containing a second command was already split into its own segment above.
	for _, tok := range seg.tokens[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if subs[tok] {
			return Verdict{Allow, ""}
		}
	}
	return Verdict{Confirm, fmt.Sprintf("%s has no read-only or build subcommand", head)}
}

// allowedOperands is how many non-flag arguments a filter may take before it is reading a file
// rather than the pipe. grep takes a pattern; the rest take none.
func allowedOperands(head string) int {
	switch head {
	case "grep", "jq":
		return 1
	default:
		return 0
	}
}

func operands(args []string, head string) int {
	takesValue := flagsTakingValue[head]
	n := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if takesValue[a] {
				i++ // its value is not an operand
			}
			continue
		}
		n++
	}
	return n
}
