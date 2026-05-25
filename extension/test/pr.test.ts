// Smoke tests for the GitHub PR pure helpers. Validates URL
// parsing, branch slugification, and PR body shaping without
// needing a vscode runtime. Mirrors agent/internal/github/pr_test.go.

import {
  MAX_BRANCH_SLUG_LEN,
  generatePRBody,
  isGitHub,
  parseRepoFromURL,
  slugifyBranch,
} from "../src/github/pr-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── parseRepoFromURL ───────────────────────────────

function testParseHTTPS(): void {
  const got = parseRepoFromURL("https://github.com/acme/widgets.git");
  assert(got?.owner === "acme" && got?.repo === "widgets", "https.git");
}

function testParseHTTPSWithoutDotGit(): void {
  const got = parseRepoFromURL("https://github.com/acme/widgets");
  assert(got?.owner === "acme" && got?.repo === "widgets", "https no .git");
}

function testParseSSH(): void {
  const got = parseRepoFromURL("git@github.com:acme/widgets.git");
  assert(got?.owner === "acme" && got?.repo === "widgets", "ssh.git");
}

function testParseSSHWithoutDotGit(): void {
  const got = parseRepoFromURL("git@github.com:acme/widgets");
  assert(got?.owner === "acme" && got?.repo === "widgets", "ssh no .git");
}

function testParseRejectsNonGitHub(): void {
  for (const bad of [
    "git@gitlab.com:acme/widgets.git",
    "https://gitea.example.com/acme/widgets.git",
    "",
    "garbage",
    "https://github.com/onlyone",
    "git@github.com:onlyone",
  ]) {
    assert(parseRepoFromURL(bad) === undefined, "should reject: " + bad);
  }
}

function testIsGitHub(): void {
  assert(isGitHub("git@github.com:a/b.git"), "ssh github");
  assert(isGitHub("https://github.com/a/b"), "https github");
  assert(!isGitHub("git@gitlab.com:a/b"), "gitlab");
  assert(!isGitHub(""), "empty");
}

// ─── slugifyBranch ──────────────────────────────────

function testSlugifyCommon(): void {
  const cases: Record<string, string> = {
    "Add JWT authentication": "feat/add-jwt-authentication",
    "Fix login bug on Safari": "fix/fix-login-bug-on-safari",
    "Refactor database layer": "feat/refactor-database-layer",
    "Bug: race condition in scheduler": "fix/bug-race-condition-in-scheduler",
  };
  for (const [input, want] of Object.entries(cases)) {
    const got = slugifyBranch(input);
    assert(got === want, `slugifyBranch(${input}) = ${got}, want ${want}`);
  }
}

function testSlugifyStripsSpecialChars(): void {
  const got = slugifyBranch("Add !!! JWT @@@ auth ### now");
  assert(!/[!@#]/.test(got), "special chars stripped: " + got);
  assert(!got.includes("--"), "dashes squeezed: " + got);
}

function testSlugifyRespectsMaxLength(): void {
  const long = "add jwt auth ".repeat(20);
  const got = slugifyBranch(long);
  assert(got.length <= MAX_BRANCH_SLUG_LEN, `len ${got.length}, cap ${MAX_BRANCH_SLUG_LEN}`);
}

function testSlugifyEmptyFallback(): void {
  const got = slugifyBranch("");
  assert(got.startsWith("feat/talyvor-"), "empty → timestamped fallback");
}

// ─── generatePRBody ─────────────────────────────────

function testGeneratePRBodyHasAllSections(): void {
  const body = generatePRBody(
    "ENG-42",
    "Add JWT authentication",
    "Wire JWT verifier into the Express middleware chain.",
    ["src/auth/jwt.ts", "src/server.ts"],
    0.1234,
  );
  for (const want of [
    "## Summary",
    "ENG-42 — Add JWT authentication",
    "Wire JWT verifier",
    "src/auth/jwt.ts",
    "## AI Cost Attribution",
    "$0.12",
    "attributed to ENG-42",
    "Opened by [Talyvor Code]",
  ]) {
    assert(body.includes(want), `body missing ${want}`);
  }
}

function testGeneratePRBodyNoIssueOmitsAttribution(): void {
  const body = generatePRBody("", "", "Refactor", ["a.ts"], 0.01);
  assert(!body.includes("attributed to"), "should not claim attribution");
}

async function main(): Promise<void> {
  testParseHTTPS();
  testParseHTTPSWithoutDotGit();
  testParseSSH();
  testParseSSHWithoutDotGit();
  testParseRejectsNonGitHub();
  testIsGitHub();
  testSlugifyCommon();
  testSlugifyStripsSpecialChars();
  testSlugifyRespectsMaxLength();
  testSlugifyEmptyFallback();
  testGeneratePRBodyHasAllSections();
  testGeneratePRBodyNoIssueOmitsAttribution();
  // eslint-disable-next-line no-console
  console.log("ok (12 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
