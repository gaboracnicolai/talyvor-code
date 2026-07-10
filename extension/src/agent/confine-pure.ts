// S11 workspace-root confinement, extracted VS Code-free so it is unit-testable (node:test) without the
// VS Code integration harness — mirrors the Go agent's confine() (agent/cmd/agent, s11_confinement_test.go).
import * as path from "path";

// absolutise resolves p under the workspace root and REFUSES (throws) any target outside it: an absolute
// path outside root, or a "../" escape. Containment is enforced here, independent of the approval prompt.
// An absolute path that legitimately lies under root is allowed.
export function absolutise(p: string, root: string): string {
  const rootAbs = path.resolve(root || ".");
  const abs = path.resolve(rootAbs, p); // absolute p honored as-is; relative p resolves under root
  const rel = path.relative(rootAbs, abs);
  if (rel === ".." || rel.startsWith(".." + path.sep) || path.isAbsolute(rel)) {
    throw new Error(`refusing path outside workspace root: ${p}`);
  }
  return abs;
}
