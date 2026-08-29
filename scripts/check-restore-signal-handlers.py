#!/usr/bin/env python3
"""Every control script that mutates a tracked file and puts it back must (a) restore in a
`finally` and (b) install a restoring SIGNAL HANDLER — because a `finally` does not run on SIGTERM.

WHY THIS EXISTS, MEASURED RATHER THAN SUPPOSED — AND IT HAPPENED TO THIS ESTATE'S OWN TABS TWICE.
talyvor-suite W1.7 (merge 78c69c8): a 2-minute command timeout SIGTERM'd a control mid-mutation,
the `finally` never ran, and the working tree was left with a shell gate reading `if true; then`.
⚠ NOTHING ABOUT THAT TREE LOOKED WRONG — the suite passed, and `git status` showed only files the
session had edited on purpose. W1.7.3 (5de27e3) then reproduced it on demand: same kill, same file,
handler present -> nothing stranded, handler removed -> the file left mutated.

⚠⚠ THIS PORT IS NOT A COPY, AND THE DIFFERENCE IS THE WHOLE REASON IT IS WORTH READING.
talyvor-suite's version defines the population as "a script whose `finally` performs a write". Run
that definition over THIS repo and it reports 3 mutate-and-restore scripts, all convertible, empty
allowlist, clean. **It is wrong by half.** The other three — w67-bypass-controls.py,
w67-credential-controls.py, w67-openai-controls.py — snapshot originals, `path.write_text(...)` a
mutation, and put the bytes back with a bare `path.write_bytes(originals[path])` on the happy path,
WITH NO `try` AT ALL. They are not protected; they are INVISIBLE, and they are STRICTLY MORE
DANGEROUS than the scripts the suite's definition does catch, because they strand the tree on any
exception, not only on a signal.

Measured across the estate read-only on 2026-08-29, `finally`-keyed population vs a population
keyed on "reads file content and writes file content":

    talyvor-suite  41 -> 52   (11 invisible)      talyvor-code   3 ->  6   (3 invisible)
    talyvor-docs   43 -> 48   ( 5 invisible)      talyvor-lens  15 -> 16   (1 invisible)
    talyvor-track  61 -> 71   (10 invisible)      ESTATE       163 -> 193 (30 invisible)

**NOT ONE of those 30 installs a handler.** The three in this repo were verified by reading them;
the other 27 are candidates a broader net found and are recorded in the queue (W3.65), not claimed
here. A population boundary that excludes the worse half of the hazard is the failure mode this
whole class of guard exists to prevent, so this port fixes the definition rather than inheriting it.

DETECTION IS SYNTACTIC (`ast`), NOT A GREP, AND THAT IS LOAD-BEARING RATHER THAN STYLISTIC. In
talyvor-suite `grep -l signal scripts/*.py` reports a script as PROTECTED whose only occurrences of
the word are four sentences of English in comments. A regex reads the documentation as the
implementation. `ast` does not see comments.

WHAT COUNTS AS A CANDIDATE: a script that both READS file content (`read_text` / `read_bytes`) and
WRITES file content (`write_text` / `write_bytes` / `writelines`, or a `shutil`/`os` copy/move).
That net is deliberately wider than "restores in a `finally`" and it will catch a pure generator
too — which is what NOT_MUTATORS is for: a place to record, with a reason someone had to type, that
a candidate does not mutate the tree. An unexplained entry is not a classification.

WHAT COUNTS AS PROTECTED: a call to `signal.signal(...)`. This does NOT verify the handler restores
the right files or re-raises; it verifies one is installed. The shape to copy is
`restore_on_signal` in any of this directory's control scripts. SIGKILL still strands and nothing in
Python can change that; the defence there is a harness that refuses to score against a red baseline.

Usage:  python3 scripts/check-restore-signal-handlers.py [--ci]
Exit 0 = every mutator restores in a `finally` and installs a handler; non-zero names what changed.
"""
import ast
import pathlib
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
SCRIPTS = REPO / "scripts"

# FLOORS ON THE DETECTOR, NOT TARGETS FOR THE TREE. If this walk stops recognising a shape it
# reports a clean directory, which is the one failure mode a guard like this has — and it is
# exactly the failure the suite's narrower definition had here. Deleting scripts is legitimate:
# lower a floor in the same diff, with the deletions visible.
# ⚠ THERE ARE TWO DETECTORS AND EACH NEEDS ITS OWN FLOOR. A single floor over their UNION is
# satisfied by EITHER of them alone — so the wider read-and-write detector could be blinded
# completely and the narrow `finally` one would hold the count up, with this guard green and the
# entire point of the port silently reverted. That is not hypothetical: control G10 in
# scripts/w363-guard-controls-r7k2.py stubs `_reads_and_writes` to `return False`, and against a
# union floor it came back GREEN. The floors are per-detector for that reason.
READWRITE_FLOOR = 8   # scripts that read file content AND write file content (the WIDE detector)
FINALLY_FLOOR = 8     # scripts that restore inside a `finally` (the NARROW detector)

# Candidates that do NOT mutate the tree, each with the reason someone had to type. This is not an
# excuse list for unconverted mutators — that is UNPROTECTED — it is the boundary of the wider net.
# R7 fails on an entry with no reason; R4 fails on an entry that is no longer a candidate.
NOT_MUTATORS: dict[str, str] = {}

# Mutators that do not yet install a handler. ⚠ MAY ONLY SHRINK. R1 stops a new one being written,
# R2 stops a fixed one being left listed. EMPTY AT THE PORT: all six were converted in the same
# merge, and an empty list is worth more than a short one only because each conversion carries its
# own SIGTERM control (S1/S2 in scripts/w363-sigterm-controls-r7k2.py) — six green scripts that were
# never actually killed would prove nothing.
UNPROTECTED: set[str] = set()

# Mutators that restore only on the happy path (no `finally`). ⚠ MAY ONLY SHRINK, same as above.
NO_FINALLY: set[str] = set()

_WRITE_ATTRS = {"write_text", "write_bytes", "writelines", "write"}
_READ_ATTRS = {"read_text", "read_bytes"}
_COPY_FUNCS = {"copy", "copy2", "copyfile", "move"}


def _write_call(n: ast.AST) -> bool:
    if not (isinstance(n, ast.Call) and isinstance(n.func, ast.Attribute)):
        return False
    if n.func.attr in _WRITE_ATTRS:
        return True
    return (n.func.attr in _COPY_FUNCS and isinstance(n.func.value, ast.Name)
            and n.func.value.id in ("shutil", "os"))


def _writes(body) -> bool:
    """True if this statement list performs a filesystem write."""
    return any(_write_call(n) for n in ast.walk(ast.Module(body=body, type_ignores=[])))


def _reads_and_writes(tree: ast.AST) -> bool:
    """The wider net: the script both reads file content and writes file content."""
    reads = any(isinstance(n, ast.Call) and isinstance(n.func, ast.Attribute)
                and n.func.attr in _READ_ATTRS for n in ast.walk(tree))
    return reads and any(_write_call(n) for n in ast.walk(tree))


def _restores_in_finally(tree: ast.AST) -> bool:
    return any(isinstance(n, ast.Try) and n.finalbody and _writes(n.finalbody)
               for n in ast.walk(tree))


def _installs_handler(tree: ast.AST) -> bool:
    """A call to `signal.signal(...)`. Comments are invisible to ast, which is the point."""
    return any(
        isinstance(n, ast.Call) and isinstance(n.func, ast.Attribute)
        and n.func.attr == "signal"
        and isinstance(n.func.value, ast.Name) and n.func.value.id == "signal"
        for n in ast.walk(tree)
    )


def main() -> int:
    read_write, in_finally, protected, errors = set(), set(), set(), []
    for path in sorted(SCRIPTS.glob("*.py")):
        try:
            tree = ast.parse(path.read_text(encoding="utf-8"))
        except (SyntaxError, UnicodeDecodeError) as e:
            # R5: a file this guard cannot read is reported, never skipped. A walk that silently
            # drops what it cannot parse reports a clean directory.
            errors.append(f"R5: {path.name} could not be parsed, so it was NOT checked: {e}")
            continue
        wide, narrow = _reads_and_writes(tree), _restores_in_finally(tree)
        if wide:
            read_write.add(path.name)
        if narrow:
            in_finally.add(path.name)
        if (wide or narrow) and _installs_handler(tree):
            protected.add(path.name)
    candidates = read_write | in_finally

    mutators = candidates - set(NOT_MUTATORS)
    fail = list(errors)

    # R3 FLOORS FIRST, because every rule below is vacuous without a population.
    if len(read_write) < READWRITE_FLOOR:
        fail.append(
            f"R3: the WIDE detector found only {len(read_write)} read-and-write scripts, floor is "
            f"{READWRITE_FLOOR}. A detector that stops recognising the shape reports a clean "
            "directory rather than a broken instrument — and this floor is measured on the wide "
            "detector ALONE precisely so the narrow one cannot hold the count up for it. If "
            "scripts were deleted, lower the floor in the same diff.")
    if len(in_finally) < FINALLY_FLOOR:
        fail.append(
            f"R3b: the NARROW detector found only {len(in_finally)} scripts restoring inside a "
            f"`finally`, floor is "
            f"{FINALLY_FLOOR}. This floor is what stops a regression to the narrower population "
            "this port exists to widen — see the header.")

    # R1: a mutator with no handler that nobody has accounted for.
    for name in sorted(mutators - protected - UNPROTECTED):
        fail.append(
            f"R1: {name} mutates a tracked file and puts it back, but installs no signal handler — "
            "a SIGTERM will strand the mutation in the working tree. Copy `restore_on_signal` from "
            "any other control script in this directory.")

    # R6: restoring only on the happy path is worse than an unprotected `finally`, not better.
    for name in sorted(mutators - in_finally - NO_FINALLY):
        fail.append(
            f"R6: {name} mutates a tracked file and restores it, but NOT in a `finally` — it strands "
            "the tree on any exception, not just on a signal. A handler alone does not cover that. "
            "Wrap the mutate/restore in try/finally. (If it does not mutate the tree, record it in "
            "NOT_MUTATORS with the reason.)")

    # R2 / R2b: an entry that has been FIXED must leave its list, or the list rots into an excuse.
    for name in sorted(UNPROTECTED & protected):
        fail.append(f"R2: {name} now installs a handler but is still listed in UNPROTECTED. "
                    "Remove the entry — this list may only shrink.")
    for name in sorted(NO_FINALLY & in_finally):
        fail.append(f"R2b: {name} now restores in a `finally` but is still listed in NO_FINALLY. "
                    "Remove the entry — this list may only shrink.")

    # R4: an entry that is no longer a candidate (deleted, renamed, restructured) is stale.
    for label, listing in (("UNPROTECTED", UNPROTECTED), ("NO_FINALLY", NO_FINALLY),
                           ("NOT_MUTATORS", set(NOT_MUTATORS))):
        for name in sorted(listing - candidates):
            fail.append(f"R4: {label} lists {name}, which is not a read-and-write script here "
                        "(deleted, renamed, or restructured). Remove the entry.")

    # R7: an unexplained NOT_MUTATORS entry is not a classification, it is a hole.
    for name, reason in sorted(NOT_MUTATORS.items()):
        if not reason.strip():
            fail.append(f"R7: NOT_MUTATORS lists {name} with no reason. An entry nobody had to "
                        "justify is how the wider net gets quietly narrowed back.")

    print(f"restore-signal-handlers: {len(candidates)} read-and-write scripts, "
          f"{len(in_finally)} restore in a `finally`, {len(protected)} install a handler, "
          f"{len(NOT_MUTATORS)} classified as non-mutators, "
          f"{len(UNPROTECTED)} listed unprotected, {len(NO_FINALLY)} listed without a `finally`")
    if fail:
        ci = len(sys.argv) > 1 and sys.argv[1] == "--ci"
        for line in fail:
            print(f"::error::{line}" if ci else line)
        return 1
    print("restore-signal-handlers: ok")
    return 0


sys.exit(main())
