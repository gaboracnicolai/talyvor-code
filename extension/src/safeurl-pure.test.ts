import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { safeBaseUrl } from "./safeurl-pure";

// THE GUARD THAT DECIDES WHERE THE CUSTOMER'S API KEY IS SENT HAD NO TEST.
//
// Measured before this file existed: "safeBaseUrl" appeared nowhere in this repository except its own
// definition in config.ts and its three call sites. Not in src, not in test/. It could not have had
// one where it lived — config.ts imports "vscode", and `node --test` cannot load such a module
// outside the editor (MODULE_NOT_FOUND, measured). So the rule moved to safeurl-pure.ts, next to the
// three sibling rules that were extracted for the same reason, and is asserted here.
//
// It is asserted against testdata/safeurl-cases.json, the file the Go port
// (agent/internal/safeurl/cases_parity_test.go) also asserts itself against. The two ports disagreed
// on 6 of these 29 cases when the file was written — in both directions — because each compared
// strings against the host shape its own runtime produces. Node keeps the IPv6 brackets and rewrites
// legacy IPv4 spellings; Go does neither.

interface SafeurlCase {
  url: string;
  safe: boolean;
  why: string;
}

/** Walks up for the shared cases. ⚠ LOUD, NOT EMPTY — a file that has moved must not leave this
 *  suite verifying nothing, which is precisely the state this rule was already in. */
function casesPath(): string {
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "testdata", "safeurl-cases.json");
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `testdata/safeurl-cases.json not found above ${__dirname} — this suite would have verified nothing`,
  );
}

function loadCases(): SafeurlCase[] {
  const p = casesPath();
  const parsed = JSON.parse(fs.readFileSync(p, "utf8")) as { cases?: SafeurlCase[] };
  const cases = parsed.cases ?? [];
  // A floor, not a count: the table may grow, but a truncated one must not look like a pass.
  assert.ok(
    cases.length >= 29,
    `${p} has ${cases.length} cases, expected at least 29 — a shrunken table proves less than it claims`,
  );
  return cases;
}

test("the TypeScript port matches the shared safeurl cases", () => {
  const cases = loadCases();

  // Both verdicts must be present, or a port that answered one way for everything would pass.
  const safe = cases.filter((c) => c.safe).length;
  assert.ok(safe > 0 && safe < cases.length, `table is one-sided (${safe}/${cases.length} safe)`);

  const wrong: string[] = [];
  for (const c of cases) {
    const got = safeBaseUrl(c.url) !== "";
    if (got !== c.safe) {
      wrong.push(`safeBaseUrl(${JSON.stringify(c.url)}) = safe:${got}, table says safe:${c.safe} — ${c.why}`);
    }
  }
  assert.deepEqual(wrong, [], `\n${wrong.join("\n")}\n`);
});

test("config.ts delegates to this rule instead of carrying its own copy", () => {
  // ⚠ THE HOLE THIS CLOSES, NAMED RATHER THAN LEFT: nothing above can see config.ts. It imports
  // "vscode", so no unit test can load it, so re-declaring the old body there would put the shipped
  // call sites back on the unfixed rule with every test still green — which is how the rule came to
  // be untested in the first place. This reads the file as TEXT, the only instrument that reaches it.
  let dir = __dirname;
  let src = "";
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "extension", "src", "config.ts");
    if (fs.existsSync(candidate)) {
      src = fs.readFileSync(candidate, "utf8");
      break;
    }
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  assert.ok(src !== "", `extension/src/config.ts not found above ${__dirname} — this check verified nothing`);
  assert.match(
    src,
    /import\s*\{\s*safeBaseUrl\s*\}\s*from\s*"\.\/safeurl-pure"/,
    "config.ts must take safeBaseUrl from safeurl-pure.ts",
  );
  assert.doesNotMatch(
    src,
    /function\s+safeBaseUrl/,
    "config.ts defines its own safeBaseUrl again — the shipped call sites would bypass the tested rule",
  );
});

test("a safe URL comes back byte-identical, not normalised", () => {
  // The caller configures a client with the RETURN value, so a guard that rewrote the URL would
  // silently point the key somewhere else. Only "" and the original are acceptable answers.
  for (const raw of ["https://lens.talyvor.com", "https://lens.example.com:8443/base", "http://[::1]:8080"]) {
    assert.equal(safeBaseUrl(raw), raw);
  }
});
