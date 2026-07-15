# Talyvor Code — BUILD STATE

Authoritative, blunt snapshot of what is actually built (recon found no such doc —
its absence was part of why a recon was needed). Update this on every substantive
build. Verdicts: **WORKING** (real logic, wired, tested) · **THIN** (real but
shallow / degraded) · **STUB** (canned/hand-off) · **ABSENT**.

Branch of record for the semantic-index work: `code-codebase-index` (off `2fd89f2`).

## Surfaces

| Surface | Path | State |
| --- | --- | --- |
| CLI agent (Go) | `agent/` | Most complete. Commands `ask chat test run index review commit pr docs shell context scope serve(MCP) models check init` — all WORKING. |
| VS Code extension (TS) | `extension/` | Broad, mostly WORKING (inline completion, chat, test-gen, agent mode, PR review, cost tracker, Track/status-bar). Thin spots: docs surface inert unless `docsUrl` set; `spec-watcher` dead; `linkDocToIssue`/`createIssueFromCode` are URL hand-offs; local cost math bills Haiku rates for all models. **Untouched this run.** |
| JetBrains plugin (Kotlin) | `jetbrains/` | ~15–20% skeleton, display-only (cannot write to the editor). Solid HTTP/SSE/settings/attribution infra. No completion/agent/file-writes. **Untouched this run.** |

## Codebase index + semantic retrieval (this build — `agent/internal/codebase`)

The recon's #1 gap ("codebase awareness = filename list + path-substring; no content
index, no embeddings, no retrieval — Cursor's defining feature, absent") is now
closed for the CLI. **WORKING:**

- **Chunker** (`chunker.go`): declaration-aware for Go (top-level func/type/const/var
  boundaries, each carrying its doc comment), overlapping line-window fallback for
  everything else. Pure.
- **Semantic index** (`semindex.go`): `BuildIndex` embeds each chunk's CONTENT (not
  its path) in batches; `Retrieve` embeds the query and cosine-ranks top-k (file +
  1-based line span). `Save`/`LoadIndex` persist a LOCAL JSON artifact.
- **Embeddings via Lens** (`internal/lens/embed.go`): `Embed` routes through
  `/v1/proxy/openai/v1/embeddings` with the SAME auth + `X-Talyvor-Issue`/`-Workspace`
  attribution headers as chat. **No local embedder, no external service.**
- **Build from repo** (`semindex_build.go`): `BuildFromRoot` reuses the confined walk,
  reads every embeddable file THROUGH `Confine` (S11), chunks, embeds. Persists to
  `<root>/.talyvor/codebase-index.json` (gitignored).
- **Retriever seam** (`retrieve.go`): `Retriever` interface + `BoundIndex` (index +
  embedder) + `RelevantContextSection` (prompt section, excludes the edited file).
- **`talyvor-code index`** command (`cmd/agent/index_cmd.go`): the deliberate,
  embed-once step. Serving commands only load + query.

### Consumers wired (this run: chat/agent ONLY, per scope fence)

- **`ask`** and **`chat`** inject the chunks most relevant to the question
  (`cmd/agent/main.go`, `retrievedContext`). Absent index → silent no-op (prior
  behavior).
- **Agent `run` per-file generation** now receives the RELEVANT SIBLING chunks
  (`generateChange(..., retriever)`), closing recon gap #2 (previously each file was
  generated blind to its siblings). Nil retriever → prior single-file behavior.
- **Completion seam**: intentionally left clean and UNWIRED (scope fence).

### Relevance source: retrieval REPLACES path-substring (for chat/agent)

`codebase.FindRelevantFiles` (path-substring, scored) is no longer the relevance
source for chat/agent — semantic retrieval is. Proven by
`TestRetrieval_ReplacesPathSubstring`: a content-only query (`"postgres pgx"`) that
path-substring MISSES (no path shares the terms) is ranked top by retrieval.
`FindRelevantFiles` survives only for (a) the agent's hallucinated-path typo-remap
(a filename resolver, not relevance) and (b) the MCP `search_codebase`/`ask_code`
tools — **deliberately NOT rewired this run** (scope = chat/agent; MCP holds a
60s-reindexed filename index and rewiring it is a clean follow-on).

## Security posture (confirmed — hardened repo, not regressed)

- **What leaves the machine:** ONLY chunk text / the query, sent to Lens for
  embedding — the SAME trust boundary as an existing chat call (same gateway, auth,
  attribution headers). No new egress, no third-party service. The index (vectors +
  chunk text) is a LOCAL file under `.talyvor/`, never uploaded.
- **S11 confinement holds:** `BuildFromRoot` reads every file through
  `codebase.Confine` (mirrors the agent write-side `confine`); the walk stays in the
  repo root. Proven by `TestBuildFromRoot_ConfinedAndChunks` (a secret file OUTSIDE
  the root never enters the index) + `TestConfine_RejectsEscape`.
- **Untouched:** MCP bearer-token/loopback auth, the cost-attribution moat, the K4
  verdict loop.

## Tests / CI

- New tests (RED-first): chunker (Go + fallback), cosine, index build+retrieve
  (ranks the right chunk; content-not-filename), save/load round-trip, confinement,
  Lens `Embed` (via httptest), retriever seam + context section, path-substring
  replacement, agent sibling-context wiring, `ask` end-to-end injection.
- **CI (4 jobs, unchanged):** agent `gofmt`/`go vet`/`go test -race` — GREEN this run
  (16 pkgs, 0 fail). Extension / JetBrains / gitleaks jobs are byte-unaffected (only
  `agent/`, `.gitignore`, and this file changed).

## Known-thin / next

- MCP `search_codebase`/`ask_code` still path-substring (+ the recon's fabricated
  relevance score) — rewire to the shared semantic index (load persisted, per-query
  embed; no 60s re-embed).
- Completion (VS Code) retrieval-grounding — the seam is clean; wire it next.
- Incremental re-index on file change (today: full rebuild via `index`).
- Path-aware embeddings (today: content-only) if disambiguation needs it.

---

# Run: iterative tool-using agent (branch `code-iterative-agent`, off b1dfbbb)

Turns the CLI agent from a bounded single-pass pipeline (index → plan once →
generate each file blind → heal ≤3) into a real ITERATIVE tool-using loop:
search → read → plan → edit → run → OBSERVE → re-plan, bounded + safe to run
unattended. New package `internal/agentloop`. TDD, red-first.

## Design forks taken (conservative + reversible — revisit any of these)
- **Tool-call transport = structured JSON in the model's text reply** (parsed +
  dispatched), NOT provider-native tool-calling. Reason: provider-agnostic (uses
  lens.Complete unchanged), reuses the repo's parsePlan/ParseHealResult pattern,
  deterministic to stub. ALTERNATIVE: native Anthropic/OpenAI tool-calling (cleaner
  structured API, per-provider client change, harder to stub). See protocol.go.
- **Wiring = opt-in `run --iterative`, existing single-pass pipeline stays the
  default** (so its tests are untouched and behavior is unchanged). "Replace the
  default" is a one-line flip deferred for review. ALTERNATIVE: default to the loop
  + move legacy behind `--single-pass` + repoint ~10 run tests. (Phase 6.)
- **run() reuses the existing runner primitive** (`sh -c` in the repo root), like the
  healer already does — command executes confined to the root; no untrusted value is
  interpolated into a shell template. ALTERNATIVE: strict arg-vector exec (safer, but
  breaks pipes/&& the model may legitimately use).

## Phase narrative
- **Phase 1 — tools scaffold** (committed). read_file/edit_file confined via
  codebase.Confine (S11, proven: escape refused + no out-of-root write); run reuses
  runner (non-zero exit = observation, not error); search_codebase = real semantic
  retrieval (nil-safe); Registry dispatch. All red-first.
- **Phase 2 — loop core** (committed). Model seam (provider-agnostic `Model`;
  `ModelFunc` stub), JSON tool-call parser (defensive), the OBSERVE/ACT loop:
  dispatch → feed result back → advance. Proven: the model SEES a tool observation on
  the next turn (feeds results back); stops on `done`; stops on the step budget;
  recovers from a malformed reply. Bounded by MaxSteps (default 20) + MaxRepeat
  (default 2) + transcript trim.
- **Phase 3 — termination + no-progress** (committed). The overnight-safety cluster:
  proven the loop aborts as StopNoProgress on (a) an edit→fail→identical-edit cycle
  (stops at step 5, not the 50-step budget), (b) the same edit every turn (step 3),
  (c) endless malformed replies; and stops cleanly on done, on budget, and surfaces a
  model-call error as StopError. An unattended run cannot burn its budget looping.
- **Phase 4 — observe→re-plan + cross-file** (committed). The headline proofs, both
  real integrations: (a) a buggy module (Add subtracts) — the agent RUNS `go test`,
  OBSERVES the failure, and re-plans to a fix, verified by a re-run; proven the fix
  was decided AFTER seeing the failure (not a blind retry). (b) server.go must use a
  constant (47213) that lives ONLY in config.go — the agent READS config.go, learns
  the value, and EDITS server.go with it. The old single-pass generateChange gets
  only the target file + task, so it can neither observe a test result nor read a
  sibling — these two capabilities are the gap this run closes.
