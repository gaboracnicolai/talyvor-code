import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { runIterativeLoop } from "./iterative-pure";

// ⚠ THE BOUND HAS TO HOLD ON THE PATH THE PRODUCT ACTUALLY TAKES. newRunTool can be perfectly
// guarded while runIterativeLoop builds an unguarded one — the tests would stay green and every
// real agent run would be unbounded. These drive the REAL loop with a fake model and assert on
// whether the command's side effect reached the disk.

/** A fake Lens that replies with one tool call, then finishes. */
function scriptedLens(replies: string[]) {
  let i = 0;
  return {
    completeWithUsage: async () => ({
      text: i < replies.length ? replies[i++] : JSON.stringify({ tool: "final", args: { answer: "done" } }),
      inputTokens: 1,
      outputTokens: 1,
    }),
    embed: async () => [] as number[][],
  };
}

function workspace(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "talyvor-iter-bound-"));
}

async function runOnce(root: string, cmd: string, confirm?: (c: string, r: string) => Promise<boolean>) {
  const lens = scriptedLens([JSON.stringify({ tool: "run", args: { cmd } })]);
  return runIterativeLoop({
    lens: lens as never,
    root,
    task: "t",
    model: "m",
    workspaceId: "ws",
    issueId: "",
    maxSteps: 3,
    confirm,
  });
}

test("the real loop does not execute a composed command", async () => {
  const root = workspace();
  await runOnce(root, "make x; touch pwned-via-loop");
  assert.equal(fs.existsSync(path.join(root, "pwned-via-loop")), false,
    "runIterativeLoop executed the composed half — the bound is not on the real path");
});

test("the real loop refuses rather than auto-approving when no confirmer is injected", async () => {
  const root = workspace();
  await runOnce(root, "touch unattended-via-loop");
  assert.equal(fs.existsSync(path.join(root, "unattended-via-loop")), false,
    "runIterativeLoop auto-approved a confirmable command with nobody to ask");
});

// ⚠ POSITIVE CONTROL. Both assertions above are that a file is ABSENT, which is trivially true if
// the loop never reached the run tool at all. This proves it does.
test("the injected confirmer is consulted and its approval reaches the shell", async () => {
  const root = workspace();
  let asked = "";
  await runOnce(root, "touch approved-via-loop", async (c, r) => {
    asked = `${c} :: ${r}`;
    return true;
  });
  assert.match(asked, /touch approved-via-loop/,
    "the confirmer was never consulted, so the tests above prove nothing about the bound");
  assert.equal(fs.existsSync(path.join(root, "approved-via-loop")), true,
    "an approved command did not run through the real loop");
});
