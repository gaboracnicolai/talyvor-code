// Package issueref recovers a Track issue identifier from a git branch name.
//
// ⚠ WHY THERE IS NO MANUAL TAGGING. Developers already name branches after the issue —
// feature/ENG-42-add-login, eng-42/fix, bugfix/ENG-7. Linear and Jira both key off exactly this.
// Reading it is free, deterministic and needs no model call, and asking a developer to pass
// --issue on every invocation is a step that will not survive contact with a real day.
//
// ⚠ IT SENDS ONLY THE IDENTIFIER, NEVER THE BRANCH NAME. This is the part most likely to go wrong.
// Branch names carry customer names, incident numbers and unreleased codenames:
// fix/acme-corp-breach-ENG-42 must transmit exactly "ENG-42" and nothing else. The function returns
// a single identifier rather than any structure containing the branch, so there is no field a
// future caller could accidentally forward. A test pins that exact branch.
//
// ⚠ IT NEVER GUESSES. No repo, a detached HEAD, main/master, or no match returns "" — and the
// caller then sends NO header at all, which Track records as unattributed. That is the correct
// outcome: an unattributed cost is honest, a cost attributed to the wrong issue is not.
package issueref

import (
	"regexp"
	"strings"
)

// pattern matches <prefix>-<number>.
//
// ⚠ IT DOES NOT ASSUME "ABC-123". Track builds an identifier as fmt.Sprintf("%s-%d",
// teamIdentifier, n) where the team identifier is a REQUIRED but otherwise UNVALIDATED string
// chosen per team (internal/team/store.go). There is no length or charset rule to rely on, so
// anchoring on three uppercase letters would silently fail every team that picked something else.
//
// The prefix is therefore letters followed by optional alphanumerics, 2–10 characters, and the
// match must be bounded so a version string inside a longer word is not mistaken for one.
var pattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])([a-z][a-z0-9]{1,9})-([0-9]+)(?:$|[^0-9])`)

// protectedBranches never carry an issue. Attributing every commit on main to whatever number
// happens to appear in its name is worse than attributing nothing.
var protectedBranches = map[string]bool{
	"main": true, "master": true, "develop": true, "development": true,
	"trunk": true, "release": true, "staging": true, "production": true,
	"HEAD": true, // detached HEAD reports this
}

// FromBranch returns the Track identifier a branch names, or "" when it names none.
//
// The identifier is UPPER-CASED: a branch called eng-42 refers to the same issue as ENG-42, and
// sending the lowercase form attributes to nothing at all because Track stores the team identifier
// as the team wrote it — conventionally upper case.
func FromBranch(branch string) string {
	b := strings.TrimSpace(branch)
	if b == "" {
		return ""
	}
	// A remote-tracking prefix is not part of the name a developer chose.
	b = strings.TrimPrefix(b, "refs/heads/")
	b = strings.TrimPrefix(b, "origin/")

	if protectedBranches[b] || protectedBranches[strings.ToLower(b)] {
		return ""
	}

	m := pattern.FindStringSubmatch(b)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[2]) + "-" + m[3]
}

// Resolve applies the precedence: an EXPLICIT identifier always wins, a detected one is used only
// when nothing was given, and neither means no attribution.
//
// ⚠ EXPLICIT WINS UNCONDITIONALLY. Someone who passed --issue has said what they mean; a branch
// name is an inference, and an inference must never override a statement.
func Resolve(explicit string, branch func() (string, error)) (identifier, source string) {
	if e := strings.TrimSpace(explicit); e != "" {
		return e, "explicit"
	}
	if branch == nil {
		return "", "none"
	}
	name, err := branch()
	if err != nil {
		// Not a repo, no commits yet, git absent — all the same answer: attribute nothing.
		return "", "none"
	}
	if id := FromBranch(name); id != "" {
		return id, "branch"
	}
	return "", "none"
}

// Describe renders the resolution for the user.
//
// ⚠ IT SHOWS THE RESOLUTION AND THE ABSENCE OF ONE. A wrong identifier is only noticeable if it is
// displayed, and "my costs are not appearing in Track" is only diagnosable if the tool says it
// attributed nothing. It NEVER prints the branch name — the identifier is the only thing derived
// from it that may be shown or sent.
func Describe(identifier, source string) string {
	switch source {
	case "explicit":
		return "issue=" + identifier + " (from --issue)"
	case "branch":
		return "issue=" + identifier + " (detected from branch)"
	default:
		return "issue=(none) — this work will be recorded in Track as unattributed"
	}
}
