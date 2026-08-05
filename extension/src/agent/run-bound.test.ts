import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { newRunTool } from "./loop-tools";

// ⚠ THE GUARD IS ONLY WORTH WHAT THE CALL SITE ENFORCES. cmdguard-pure can be perfect and change
// nothing if `run` does not consult it, so these drive the TOOL and assert on whether a command
// ACTUALLY EXECUTED — observed through its side effect on disk, never through the returned text.
// A test that only checked the message would pass on a tool that printed "REFUSED" and ran it.

function workspace(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "talyvor-run-bound-"));
}

/** ran reports whether the command's side effect happened. */
function ran(root: string, marker: string): boolean {
  return fs.existsSync(path.join(root, marker));
}

// ⚠ RED-FIRST, BOTH WAYS: an ordinary command must still run. A guard that stopped everything would
// pass every refusal test and destroy the product.
test("an ordinary build command still runs unattended", async () => {
  const root = workspace();
  const tool = newRunTool(root, 20_000);
  const out = await tool.run(JSON.stringify({ cmd: "make ordinary-marker" }));
  // `make` is allowlisted with any arguments; with no Makefile it fails, which is fine — the point
  // is that it EXECUTED rather than being stopped.
  assert.match(out, /exit \d+/, `an allowlisted command did not run:\n${out}`);
  assert.doesNotMatch(out, /REFUSED|NOT RUN/, `an ordinary command was blocked:\n${out}`);
});

// ⚠ THE PREFIX-MATCH TRAP, AT THE CALL SITE. This starts with an allowed verb.
test("a composed command does not execute its second half", async () => {
  const root = workspace();
  const tool = newRunTool(root, 20_000);
  const out = await tool.run(JSON.stringify({ cmd: "make x; touch pwned" }));
  assert.equal(ran(root, "pwned"), false, `the composed half EXECUTED:\n${out}`);
  assert.match(out, /NOT RUN|REFUSED/, `it was not reported as blocked:\n${out}`);
});

// ⚠ NO INTERACTIVE SURFACE MEANS REFUSE, NEVER AUTO-APPROVE. An agent running with no one watching
// is exactly where an unattended `curl | sh` would be least noticed, so the absence of a human is
// the strongest reason to stop rather than the reason to proceed.
test("with no confirmer, a confirmable command is refused rather than approved", async () => {
  const root = workspace();
  const tool = newRunTool(root, 20_000); // no confirm seam supplied
  const out = await tool.run(JSON.stringify({ cmd: "touch unattended" }));
  assert.equal(ran(root, "unattended"), false, `it ran with nobody to ask:\n${out}`);
  assert.match(out, /no interactive/i, `the reason does not say why it could not be confirmed:\n${out}`);
});

test("a declined confirmation does not run", async () => {
  const root = workspace();
  let asked = "";
  const tool = newRunTool(root, 20_000, async (cmd, reason) => {
    asked = `${cmd} :: ${reason}`;
    return false;
  });
  const out = await tool.run(JSON.stringify({ cmd: "touch declined" }));
  assert.equal(ran(root, "declined"), false, `a declined command ran:\n${out}`);
  assert.match(asked, /touch declined/, "the prompt did not show the exact command");
  assert.ok(asked.includes("::") && asked.split("::")[1].trim().length > 0,
    "the prompt did not say what was unusual about it");
});

test("an approved confirmation does run", async () => {
  const root = workspace();
  const tool = newRunTool(root, 20_000, async () => true);
  const out = await tool.run(JSON.stringify({ cmd: "touch approved" }));
  assert.equal(ran(root, "approved"), true, `an approved command did not run:\n${out}`);
});

// ⚠ AN UNPARSEABLE COMMAND IS REFUSED EVEN WHEN SOMEONE IS THERE TO SAY YES. Showing a user
// `make $(cat /tmp/x)` and calling their yes informed consent would be worse than refusing.
test("substitution is refused outright and never offered for confirmation", async () => {
  const root = workspace();
  let asked = false;
  const tool = newRunTool(root, 20_000, async () => {
    asked = true;
    return true; // an always-yes confirmer: if it were consulted, the command would run
  });
  const out = await tool.run(JSON.stringify({ cmd: "touch subst$(echo 1)" }));
  assert.equal(asked, false, "an unparseable command was offered for confirmation");
  assert.match(out, /REFUSED/, `it was not refused:\n${out}`);
  assert.equal(fs.readdirSync(root).length, 0, `something was created in the workspace:\n${out}`);
});

// ⚠ POSITIVE CONTROL ON THE REFUSAL ITSELF. Every assertion above is that a file is ABSENT, and a
// file is trivially absent if `touch` never works here for an unrelated reason — a broken shell, a
// read-only temp dir, a wrong cwd. This proves the exact side effect the refusals rely on is
// otherwise reachable, so their absence means something.
test("the marker file the refusal tests rely on is genuinely creatable", async () => {
  const root = workspace();
  const tool = newRunTool(root, 20_000, async () => true);
  await tool.run(JSON.stringify({ cmd: "touch control-marker" }));
  assert.equal(ran(root, "control-marker"), true,
    "touch does not work in this workspace, so every refusal test above proves nothing");
});
