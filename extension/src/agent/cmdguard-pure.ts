// cmdguard-pure decides whether a model-authored shell command may run unattended.
//
// ⚠ WHAT IT IS PROTECTING. loop-tools.ts hands a model-authored string to `sh -c` with a cwd and a
// timeout and nothing else. That shell reaches ~/.ssh, the environment and the network. The
// extension is not published yet, which is the only reason this is cheap to add — and the last
// moment it will be.
//
// ⚠ THIS IS A PORT OF agent/internal/cmdguard, NOT A SECOND DESIGN. Two guards that disagree are
// worse than one, because the weaker one is the one that matters. The Go parser cannot be called
// from TypeScript — separate runtimes, and compiling it to WASM to gate a VS Code extension's shell
// calls would put a build toolchain on a security path for no proportionate gain. So the design is
// ported and testdata/cmdguard-corpus.json holds both to the same verdicts; a drift fails a build.
//
// The reasoning below is the same as the Go package's, kept here because a reader of this file
// should not have to find the other one to know why it works this way:
//
// ⚠ AN ALLOWLIST, NOT A DENYLIST. The harmful space is unbounded and `sh -c` composes, so a
// denylist is a list of the attacks someone already thought of; the first one nobody wrote down is
// the one that runs. Default-deny makes a missing entry cost a prompt, not an execution.
//
// ⚠ IT PARSES INSTEAD OF PREFIX-MATCHING. `go test; curl evil.com` and `go test && rm -rf ~` both
// begin with an allowed verb, so any check that looks at the start of the string says yes to both.
//
// ⚠ A COMMAND IT CANNOT PARSE IS REFUSED, NEVER CONFIRMED. `$(...)` and backticks can produce
// anything at run time, so no prompt could show a user what they are agreeing to.

export type Decision = "allow" | "confirm" | "refuse";

export interface Verdict {
  decision: Decision;
  /** reason names the SEGMENT that forced the decision, so a prompt can say what is unusual
   *  rather than showing the whole command and asking the user to spot it. */
  reason: string;
}

// ⚠ THE THREE TABLES BELOW ARE EXPORTED ONLY SO cmdguard-tables.test.ts CAN COMPARE THEM TO THE
// SHARED MANIFEST. Nothing else imports them; the guard is `check`. They are the POPULATION the
// corpus only samples — see testdata/cmdguard-tables.json for what that cost.
//
// allowedHeads maps a command to the subcommands that are read-only or build-shaped. An empty set
// means the command is allowed with any arguments.
//
// ⚠ vcs is READ-ONLY on purpose. `git status/diff/log` are how an agent orients; `git push`,
// `git commit`, `git checkout` and `git clean` change or destroy work and go through confirmation.
export const allowedHeads: Record<string, ReadonlySet<string>> = {
  go: new Set(["build", "test", "vet", "fmt", "list", "mod"]),
  gofmt: new Set(),
  "golangci-lint": new Set(),
  cargo: new Set(["build", "test", "check", "clippy", "fmt"]),
  mvn: new Set(),
  gradle: new Set(),
  make: new Set(),
  tsc: new Set(),
  eslint: new Set(),
  prettier: new Set(),
  ruff: new Set(),
  pytest: new Set(),
  jest: new Set(),
  vitest: new Set(),
  npm: new Set(["test", "run", "ci", "ls"]),
  pnpm: new Set(["test", "run", "build", "lint", "typecheck", "install"]),
  yarn: new Set(["test", "run", "build", "lint"]),
  git: new Set([
    "status", "diff", "log", "show", "branch",
    "rev-parse", "ls-files", "describe", "blame", "remote",
  ]),
};

// pipeFilters may appear AFTER a pipe. They read stdin, and the operand check below denies them a
// file — `go test | grep FAIL` is ordinary; `grep secret ~/.ssh/id_rsa` is not, and the only
// difference is an operand.
export const pipeFilters = new Set(["head", "tail", "wc", "sort", "uniq", "grep", "cut", "tr", "jq"]);

// flagsTakingValue are the flags that consume the NEXT token, PER COMMAND — `-n` takes a value for
// `head` and takes none for `grep`. A single shared set got this wrong in the permissive direction
// in the Go implementation: `grep -n secret ~/.ssh/id_rsa` had its file counted as `-n`'s value, so
// the operand check saw one operand and allowed a read of the key. Wrong the other way costs only a
// confirmation, so the split is kept here too.
export const flagsTakingValue: Record<string, ReadonlySet<string>> = {
  grep: new Set(["-e", "--regexp", "-m", "--max-count"]),
  head: new Set(["-n", "-c"]),
  tail: new Set(["-n", "-c"]),
  cut: new Set(["-d", "-f"]),
  sort: new Set(["-k", "-t"]),
  jq: new Set(),
};

interface Segment {
  tokens: string[];
  afterPipe: boolean;
}

class ParseError extends Error {}

/** check classifies a model-authored command. */
export function check(command: string): Verdict {
  const cmd = command.trim();
  if (cmd === "") return { decision: "refuse", reason: "empty command" };

  // ⚠ SUBSTITUTION IS UNPARSEABLE BY CONSTRUCTION. What `$(...)` expands to is not known until a
  // shell runs it, so neither this check nor a human reading a prompt can know what would run.
  if (cmd.includes("$(") || cmd.includes("`")) {
    return {
      decision: "refuse",
      reason: "command substitution ($(…) or backticks) — what it expands to is not knowable before it runs",
    };
  }
  if (cmd.includes("<(") || cmd.includes(">(") || cmd.includes("<<")) {
    return {
      decision: "refuse",
      reason: "process substitution or here-document — the effective command is not knowable in advance",
    };
  }

  let segments: Segment[];
  try {
    segments = split(cmd);
  } catch (e) {
    return { decision: "refuse", reason: e instanceof Error ? e.message : String(e) };
  }

  for (const seg of segments) {
    const v = checkSegment(seg);
    if (v.decision !== "allow") return v;
  }
  return { decision: "allow", reason: "" };
}

/** split breaks a command on the shell operators that start a NEW command, honouring quotes so a
 *  separator inside a string is not treated as one. It understands a small grammar deliberately and
 *  refuses anything outside it, because the alternative — guessing — is how a prefix match fails. */
function split(cmd: string): Segment[] {
  const segs: Segment[] = [];
  let cur = "";
  let afterPipe = false;
  let quote = "";

  const flush = (nextAfterPipe: boolean): void => {
    const text = cur.trim();
    cur = "";
    if (text !== "") {
      const toks = tokenize(text);
      if (toks.length > 0) segs.push({ tokens: toks, afterPipe });
    }
    afterPipe = nextAfterPipe;
  };

  const rs = [...cmd];
  for (let i = 0; i < rs.length; i++) {
    const c = rs[i];
    if (quote !== "") {
      if (c === quote) quote = "";
      cur += c;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      cur += c;
    } else if (c === "\\") {
      // A line continuation or escaped separator changes what the shell sees; rather than model
      // every escape, refuse and let the user confirm explicitly.
      throw new ParseError("backslash escaping — refused rather than guessed");
    } else if (c === ";" || c === "\n") {
      flush(false);
    } else if (c === "&") {
      if (i + 1 < rs.length && rs[i + 1] === "&") {
        i++;
        flush(false);
      } else {
        // A lone & backgrounds the command; it then outlives the timeout that is the only bound
        // this tool had.
        throw new ParseError("backgrounding (&) — the process would outlive the run timeout");
      }
    } else if (c === "|") {
      if (i + 1 < rs.length && rs[i + 1] === "|") {
        i++;
        flush(false);
      } else {
        flush(true);
      }
    } else {
      cur += c;
    }
  }
  if (quote !== "") throw new ParseError("unbalanced quote — the command cannot be parsed");
  flush(false);
  if (segs.length === 0) throw new ParseError("no command found");
  return segs;
}

/** tokenize splits one simple command into tokens, stripping surrounding quotes. */
function tokenize(s: string): string[] {
  const toks: string[] = [];
  let cur = "";
  let quote = "";
  const push = (): void => {
    if (cur.length > 0) {
      toks.push(cur);
      cur = "";
    }
  };
  for (const c of s) {
    if (quote !== "") {
      if (c === quote) quote = "";
      else cur += c;
    } else if (c === "'" || c === '"') {
      quote = c;
    } else if (c === " " || c === "\t") {
      push();
    } else {
      cur += c;
    }
  }
  if (quote !== "") throw new ParseError("unbalanced quote — the command cannot be parsed");
  push();
  return toks;
}

function checkSegment(seg: Segment): Verdict {
  if (seg.tokens.length === 0) return { decision: "refuse", reason: "empty segment" };
  const head = seg.tokens[0];

  // ⚠ A REDIRECT WRITES OUTSIDE THE COMMAND'S OWN OUTPUT. `go test > /tmp/x` is ordinary and
  // `printf ... > ~/.ssh/authorized_keys` is not, and both are parseable — so this is a
  // confirmation, not a refusal: the user is shown exactly where it writes.
  for (const t of seg.tokens) {
    if (t.startsWith(">") || t.startsWith("<")) {
      return { decision: "confirm", reason: `${head} redirects output (${t})` };
    }
  }

  // An env assignment prefix (FOO=bar cmd) hides the real head.
  if (head.includes("=") && !head.startsWith("-")) {
    return { decision: "confirm", reason: `environment assignment before the command (${head})` };
  }
  // A path-qualified command is not the tool of the same name.
  if (head.includes("/") || head.includes("\\")) {
    return { decision: "confirm", reason: `runs a program by path (${head}), not a known toolchain command` };
  }

  if (seg.afterPipe) {
    if (!pipeFilters.has(head)) {
      return { decision: "confirm", reason: `${head} is not a read-only filter` };
    }
    // ⚠ A FILTER WITH A FILE OPERAND IS NOT READING THE PIPE. `grep x file` opens the file
    // regardless of stdin, so the operand count is what separates the two.
    if (operands(seg.tokens.slice(1), head) > allowedOperands(head)) {
      return { decision: "confirm", reason: `${head} is given a file to read, not just the piped input` };
    }
    return { decision: "allow", reason: "" };
  }

  const subs = allowedHeads[head];
  if (subs === undefined) {
    return { decision: "confirm", reason: `${head} is not a build, test, lint or version-control read command` };
  }
  if (subs.size === 0) return { decision: "allow", reason: "" };

  // ⚠ LOOK FOR AN ALLOWED SUBCOMMAND, DO NOT GUESS BY POSITION. `pnpm --filter web test` puts the
  // flag's VALUE where a positional scan expects the subcommand, and reading "web" there refused an
  // ordinary monorepo command. Scanning for a member of the allowlist is both more permissive on
  // honest input and no weaker on hostile input: the head's own allowlist still bounds it, and
  // anything containing a second command was already split into its own segment above.
  for (const tok of seg.tokens.slice(1)) {
    if (tok.startsWith("-")) continue;
    if (subs.has(tok)) return { decision: "allow", reason: "" };
  }
  return { decision: "confirm", reason: `${head} has no read-only or build subcommand` };
}

/** allowedOperands is how many non-flag arguments a filter may take before it is reading a file
 *  rather than the pipe. grep takes a pattern; the rest take none. */
function allowedOperands(head: string): number {
  return head === "grep" || head === "jq" ? 1 : 0;
}

function operands(args: string[], head: string): number {
  const takesValue = flagsTakingValue[head] ?? new Set<string>();
  let n = 0;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a.startsWith("-")) {
      if (takesValue.has(a)) i++; // its value is not an operand
      continue;
    }
    n++;
  }
  return n;
}
