// Pure helpers for the shell-command generation flow. Mirrors
// the Go side (agent/internal/shell) so both surfaces produce
// the same prompt shape + post-processing.

// detectShell extracts the shell name from a $SHELL-style path
// or a raw shell token. Returns "bash" as the safe default. We
// don't try to split on whitespace because Windows shell paths
// legitimately contain spaces ("C:\\Program Files\\…").
export function detectShell(shellPath: string | undefined): string {
  if (!shellPath) return "bash";
  const base = shellPath.split(/[\\/]/).filter((p) => p.length > 0).pop();
  if (!base) return "bash";
  // Strip a possible -i / --login style trailing argument
  // attached by some VS Code shell-integration shims.
  const head = base.split(/\s+/)[0];
  return head.replace(/\.exe$/i, "");
}

// detectOS maps process.platform to the friendly name the model
// attends to better than the raw token.
export function detectOS(platform: NodeJS.Platform | string): string {
  switch (platform) {
    case "darwin":
      return "macOS";
    case "linux":
      return "Linux";
    case "win32":
    case "windows":
      return "Windows";
    case "freebsd":
      return "FreeBSD";
    case "openbsd":
      return "OpenBSD";
  }
  return String(platform);
}

// buildShellPrompt constrains the model to one command, no
// fences, no prose. Same rules as the Go side so we get
// consistent output across the CLI and the extension.
export function buildShellPrompt(
  description: string,
  shell: string,
  osName: string,
): string {
  return `You are an expert shell command generator.
Generate a single shell command for the given task.

Rules:
- Return ONLY the command, nothing else
- No markdown, no backticks, no explanation
- Use ${shell} syntax
- The command must work on ${osName}
- Prefer safe commands (no -rf without confirmation)
- For complex tasks: pipe commands together
- If the task is impossible in one command, return the most practical approach

Task: ${description}`;
}

// preambles mirror the Go regex list. Order matters — the
// stripping loop runs each in turn until the string stabilises.
const preambles: RegExp[] = [
  /^(here(?:'s| is)?\s+(the\s+)?(command|shell command)\s*:?\s*)/i,
  /^(the command (you want|is)\s*:?\s*)/i,
  /^(command\s*:?\s*)/i,
];

// stripGenerated removes fences, preambles, and surrounding
// whitespace. Loops until stable because "Here is the command:\n
// ```bash\n…\n```" needs the preamble out before the fence is
// reachable.
export function stripGenerated(s: string): string {
  let out = s.trim();
  for (let i = 0; i < 4; i++) {
    let next = out;
    for (const re of preambles) {
      next = next.replace(re, "");
    }
    next = next.trim();
    if (next.startsWith("```")) {
      const nl = next.indexOf("\n");
      if (nl >= 0) next = next.slice(nl + 1);
    }
    if (next.endsWith("```")) {
      next = next.slice(0, -3);
    }
    next = next.trim();
    if (next.startsWith("`") && next.endsWith("`") && next.length > 1) {
      next = next.slice(1, -1).trim();
    }
    if (next === out) break;
    out = next;
  }
  // Take the first non-empty line — commands should be single-line.
  for (const line of out.split("\n")) {
    if (line.trim() !== "") return line.trim();
  }
  return out.trim();
}

// dangerousPatterns mirrors the Go list. Same advisory role —
// match → extra "are you sure?" warning, never a hard block.
const dangerousPatterns: RegExp[] = [
  /\brm\b[^|;\n]*\s-[rR]?[fF]?[rR]?\s+(\/|~|\/\*|\/etc\b|\/usr\b|\/var\b|\/home\b)/,
  /\brm\b\s+-[a-zA-Z]*[fF][a-zA-Z]*\s+\/(\s|$|\*)/,
  /\bdd\b[^|;\n]*\bof=\/dev\//,
  /\bchmod\b\s+(-[a-zA-Z]+\s+)?777\b\s+(\/|~|\/etc\b|\/usr\b)/,
  /\bmkfs\.[a-z0-9]+\b\s+\/dev\//,
  />\s*\/dev\/sd[a-z]/,
  /:\(\)\s*{\s*:\|:&\s*}\s*;\s*:/,
  /\bshutdown\b\s+-[a-zA-Z]*[hH]/,
  /\bcurl\b[^|;\n]*\|\s*(sudo\s+)?sh\b/,
];

export function isCommandSafe(command: string): boolean {
  return !dangerousPatterns.some((re) => re.test(command));
}
