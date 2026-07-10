import { test } from "node:test";
import assert from "node:assert/strict";
import * as path from "path";
import { absolutise } from "./confine-pure";

// S11 behavioral test — the TS twin of the Go agent's s11_confinement_test.go. Previously the TS
// containment only COMPILED; this exercises it: a "../" escape or an absolute path outside the workspace
// root must be REFUSED (throws), while an in-root path is allowed and resolved under the root.
const root = path.resolve("/home/user/project");

test("absolutise refuses a ../ escape", () => {
  assert.throws(() => absolutise("../evil.txt", root), /refusing path outside workspace root/);
});

test("absolutise refuses a nested ../ climb out of root", () => {
  assert.throws(() => absolutise("sub/../../escape.txt", root), /refusing path outside workspace root/);
});

test("absolutise refuses an absolute path outside root", () => {
  assert.throws(() => absolutise("/etc/passwd", root), /refusing path outside workspace root/);
});

test("absolutise allows an in-root relative path", () => {
  assert.equal(absolutise("sub/ok.txt", root), path.join(root, "sub", "ok.txt"));
});

test("absolutise allows an absolute path that legitimately lies under root", () => {
  assert.equal(absolutise(path.join(root, "sub", "ok.txt"), root), path.join(root, "sub", "ok.txt"));
});
