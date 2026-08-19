#!/usr/bin/env bash
# Positive controls for the W4.16 scope claim/wiring guard (tab-7k3q).
#
# Every control mutates ONE thing, states the prediction BEFORE running, measures, and reverts.
# A guard is only worth its green if each direction it claims to cover has been shown red.
#
# ⚠ IT SNAPSHOTS BY COPY, NOT BY `git checkout --`, AND THAT IS THE WHOLE REASON THIS COMMENT
# EXISTS. The first version of this script reverted each mutation with `git checkout -- <file>`,
# which restores from the INDEX. The fix under test was not yet committed, so every revert silently
# threw the fix away and restored main's prose — and the run then measured main, not the branch.
# It read as a guard defect (C4 green when red was predicted) and was a harness defect. A control
# harness that mutates a working tree must restore the WORKING TREE, from bytes it captured itself.
set -uo pipefail

cd "$(dirname "$0")/.."
ROOT="$PWD"
GUARD='TestScopeFilterClaimMatchesItsWiring'
FACT='TestActiveScopeDoesNotNarrow'
PASSED=0
FAILED=0

SNAP=$(mktemp -d)
trap 'rm -rf "$SNAP"' EXIT

WATCHED=(
  agent/cmd/agent/main.go
  agent/internal/scope/scope.go
  agent/internal/scope/filter_wiring_test.go
  extension/src/commands/scope-command.ts
)

snapshot() {
  local f
  for f in "${WATCHED[@]}"; do
    mkdir -p "$SNAP/$(dirname "$f")"
    cp "$ROOT/$f" "$SNAP/$f"
  done
}
restore() {
  local f
  for f in "${WATCHED[@]}"; do
    cp "$SNAP/$f" "$ROOT/$f"
  done
}

run() { (cd "$ROOT/agent" && go test -count=1 -run "$1" ./internal/scope/ >/dev/null 2>&1); }

# check <id> <expected: RED|GREEN> <test> <description>
check() {
  local id="$1" want="$2" test="$3" desc="$4" got
  if run "$test"; then got=GREEN; else got=RED; fi
  if [ "$got" = "$want" ]; then
    printf '  %-4s %-5s (predicted %-5s) OK   %s\n' "$id" "$got" "$want" "$desc"
    PASSED=$((PASSED + 1))
  else
    printf '  %-4s %-5s (predicted %-5s) MISS %s\n' "$id" "$got" "$want" "$desc"
    FAILED=$((FAILED + 1))
  fi
}

snapshot

echo "-- baseline --"
check C0a GREEN "$GUARD" "unmutated tree: claim absent, 0 callers -- they agree"
check C0b GREEN "$FACT" "unmutated tree: an active scope does not narrow the index"

echo "-- the claim returns, in each of the three files the census watches --"

perl -0pi -e 's/Scope cleared\. The prompt no longer names a focus area\./Scope cleared. All files in context./' agent/cmd/agent/main.go
check C1 RED "$GUARD" "agent/cmd/agent/main.go re-states the narrowing claim"
restore

# C2 also proves the census crosses the runtime boundary: a Go test reading a TypeScript file it
# has no import relationship with, in a different build system.
perl -0pi -e 's/Scope cleared\. The prompt no longer names a focus area\./Scope cleared. All files in context./' extension/src/commands/scope-command.ts
check C2 RED "$GUARD" "extension/src/commands/scope-command.ts re-states it (cross-runtime reach)"
restore

perl -0pi -e 's/IT DOES NOT RESTRICT WHICH FILES/IT IS applied as a file filter and DOES RESTRICT WHICH FILES/' agent/internal/scope/scope.go
check C3 RED "$GUARD" "agent/internal/scope/scope.go re-states it"
restore

echo "-- the OTHER direction: the behaviour returns and the prose does not --"

# C4 plants a real production caller OUTSIDE package scope (the census excludes the package's own
# definitions by design). The guard must now demand the sentences back. This is the direction a
# claim-only check cannot see, and the reason the census counts callers at all.
cat > agent/internal/projectctx/w416_control_c4.go <<'GO'
package projectctx

import (
	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/scope"
)

// Synthetic production caller planted by the W4.16 control script.
func w416ControlC4(sm *scope.ScopeManager, f []codebase.FileInfo) []codebase.FileInfo {
	return sm.FilterFiles(f)
}

var _ = w416ControlC4
GO
check C4 RED "$GUARD" "a production caller appears while the prose still denies filtering"
rm -f agent/internal/projectctx/w416_control_c4.go
restore

echo "-- vacuity: the guard must fail LOUDLY when it can no longer read what it names --"

# C5 removes the anchor the census locates itself by. Without this branch the check would pass
# silently on a file it is no longer reading -- green because blind, which is the failure this
# queue has caught three times.
perl -0pi -e 's/Scope cleared\. The prompt no longer names a focus area\./Focus area dropped./' agent/cmd/agent/main.go
check C5 RED "$GUARD" "anchor gone from main.go -- reported as broken, not as passing"
restore

mv extension/src/commands/scope-command.ts extension/src/commands/scope-command.ts.c6
check C6 RED "$GUARD" "a watched file is unreadable -- reported, not skipped"
mv extension/src/commands/scope-command.ts.c6 extension/src/commands/scope-command.ts
restore

echo "-- the fact-pin must not be inert either --"

# C7 inverts the fact-pin's expectation, which is what wiring the filter would do to the measured
# population. It has to notice, or it is a test that stays green through the change it exists for.
perl -0pi -e 's/if !sawBilling \{/if sawBilling { \/* C7 *\//' agent/internal/scope/filter_wiring_test.go
check C7 RED "$FACT" "inverted expectation -- the fact-pin is reading the real population"
restore

# C8 blinds the census. `scanned < 20` is the only thing between a green and a walk that reached
# nothing; prove it fires rather than letting 0 callers read as agreement.
perl -0pi -e 's/if scanned < 20 \{/if scanned < 100000 {/' agent/internal/scope/filter_wiring_test.go
check C8 RED "$GUARD" "census scan-count floor fires when the walk is blinded"
restore

echo
echo "-- final state must equal the starting state, byte for byte --"
DIRTY=0
for f in "${WATCHED[@]}"; do
  if ! cmp -s "$SNAP/$f" "$ROOT/$f"; then
    echo "  NOT RESTORED: $f"
    DIRTY=1
  fi
done
if [ -e agent/internal/projectctx/w416_control_c4.go ]; then
  echo "  NOT RESTORED: agent/internal/projectctx/w416_control_c4.go (planted file survived)"
  DIRTY=1
fi
if [ "$DIRTY" -eq 0 ]; then echo "  tree restored, all ${#WATCHED[@]} watched files byte-identical"; else FAILED=$((FAILED + 1)); fi

echo
echo "controls: ${PASSED} matched prediction, ${FAILED} did not"
[ "$FAILED" -eq 0 ]
