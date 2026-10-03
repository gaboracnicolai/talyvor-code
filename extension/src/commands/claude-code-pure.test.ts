import { test } from "node:test";
import assert from "node:assert/strict";
import { CLAUDE_EXEC_ARGS, cliEnv, findOnPath } from "./claude-code-pure";

test("findOnPath returns the first PATH entry holding the binary", () => {
  const present = new Set(["/opt/bin/talyvor-code", "/usr/local/bin/talyvor-code"]);
  const found = findOnPath("talyvor-code", "/usr/bin::/usr/local/bin:/opt/bin", "darwin", (f) => present.has(f));
  assert.equal(found, "/usr/local/bin/talyvor-code");
  assert.equal(findOnPath("talyvor-code", "/usr/bin", "linux", (f) => present.has(f)), undefined);
});

test("findOnPath finds the Windows .exe through PATHEXT", () => {
  const found = findOnPath(
    "talyvor-code",
    "C:\\Windows;C:\\tools",
    "win32",
    (f) => f.toLowerCase() === "c:\\tools\\talyvor-code.exe", // Windows paths match case-insensitively
    ".COM;.EXE",
  );
  assert.equal(found, "C:\\tools\\talyvor-code.EXE");
});

test("the terminal runs Claude Code under the sidecar with the Lens settings in its environment", () => {
  assert.deepEqual(CLAUDE_EXEC_ARGS, ["exec", "--", "claude"]);
  assert.deepEqual(
    cliEnv({ url: "https://lens.example", apiKey: "k", workspaceId: "ws_1", activeIssue: "ENG-42" }),
    {
      TALYVOR_LENS_URL: "https://lens.example",
      TALYVOR_LENS_API_KEY: "k",
      TALYVOR_WORKSPACE_ID: "ws_1",
      TALYVOR_ISSUE: "ENG-42",
    },
  );
  // No active issue: left out, so the CLI detects it from the branch instead of being told "none".
  assert.equal("TALYVOR_ISSUE" in cliEnv({ url: "u", apiKey: "k", workspaceId: "", activeIssue: " " }), false);
});
