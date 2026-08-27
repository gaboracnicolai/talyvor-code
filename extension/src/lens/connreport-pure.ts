// connreport-pure — the decision behind "Talyvor: Test Lens Connection", extracted from
// extension.ts so `node --test` can reach it.
//
// ⚠ WHY IT IS A SEPARATE FILE, WHICH IS ALSO WHY IT WAS WRONG: the decision lived inline in
// extension.ts, and extension.ts imports "vscode" — a module `node --test` cannot load outside the
// editor. So the sentence the user is shown when they ask whether their setup works had no test at
// all, in any runtime. Same reason safeurl-pure.ts and secrets-pure.ts exist.

/** What a credential probe could establish. `unknown` is not a soft `bad` — see connectionReport. */
export type CredentialVerdict = "ok" | "rejected" | "unknown";

export interface ConnectionInput {
  /** GET /healthz succeeded. NOTE: Lens serves /healthz UNAUTHENTICATED. */
  available: boolean;
  /** Version string from /healthz, or "unknown". */
  version: string;
  /** Result of the authenticated probe. */
  verdict: CredentialVerdict;
  /** HTTP status that produced the verdict (0 when there was no response). */
  status: number;
}

export interface ConnectionReport {
  kind: "info" | "error";
  message: string;
}

/**
 * verdictForStatus maps an HTTP status from GET /v1/auth/me onto a verdict.
 *
 * FAIL-OPEN BY CONSTRUCTION: only 401/403 accuses the key. Asserted against
 * testdata/credential-verdict-cases.json, the same file the Go and Kotlin ports assert
 * themselves against — see credential-cases.test.ts.
 */
export function verdictForStatus(status: number): CredentialVerdict {
  if (status === 401 || status === 403) return "rejected";
  if (status >= 200 && status < 300) return "ok";
  return "unknown";
}

export function connectionReport(input: ConnectionInput): ConnectionReport {
  if (!input.available) {
    return {
      kind: "error",
      message: "❌ Cannot connect to Lens — check the URL and your network.",
    };
  }
  // Reachable. That is a statement about the URL and the network ONLY, because /healthz takes no
  // credential — so the report must not upgrade it into a statement about the key.
  if (input.verdict === "rejected") {
    return {
      kind: "error",
      message:
        `❌ Lens is reachable, but it rejected your API key (HTTP ${input.status}). ` +
        "Check talyvor.lensApiKey — the URL is fine.",
    };
  }
  if (input.verdict === "ok") {
    return { kind: "info", message: `✅ Connected to Lens v${input.version} — API key verified` };
  }
  // "unknown": the authenticated probe could not answer (an older Lens with no /v1/auth/me, a
  // server-error reply, a proxy that ate it). Report EXACTLY what was reported before this probe existed. Claiming the
  // key is bad here would red a working install; claiming it is verified would be the same false ✓
  // one level down. Say neither.
  return { kind: "info", message: `✅ Connected to Lens v${input.version}` };
}
