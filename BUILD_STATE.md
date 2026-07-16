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
- **Phase 5 — self-heal folds into the loop** (committed). A failing test is just
  another OBSERVE the model re-plans on — not a bolted-on ≤3 retry. Proven richer
  than the old healer: the heal RUNS the test, observes the failure, SEARCHES the
  codebase for the right helper, and edits (run→observe→search→edit→run→done). The
  old healer could only regenerate the changed file from the raw error text; it had
  no search/read. Self-heal is now native loop behavior with the full tool set.
- **Phase 6 — wired into `run --iterative`** (committed). New `cmd/agent/iterative.go`:
  lensModel adapter (loop turns are Lens completions with the X-Talyvor-Issue/-Workspace
  attribution, feature "agent-loop") + runIterativeAgent (builds the confined tool set +
  retriever, runs the loop, prints Stop/Steps/Summary/EditedFiles). `run --iterative`
  (opt-in; `--max-steps`, default 20) short-circuits the single-pass pipeline. `--dry-run`
  is noted-and-ignored under --iterative (the loop must apply edits to run + observe).
  End-to-end CLI test (mocked Lens scripting tool calls) proves the edit is applied and a
  clean done is reported. Existing single-pass `run` tests untouched + green.
- **Phase 7 — confinement hardening** (committed). S11 proven to hold on EVERY tool
  under an ADVERSARIAL model driving the loop: a model that deliberately tries to
  read/overwrite/create files OUTSIDE the root gets nothing — every escape returns a
  refusal observation (loop re-plans, never crashes), the out-of-root secret NEVER
  enters the transcript, the outside file is unchanged, and no file is created
  outside. run() executes in the repo root (proven: a relative write lands inside).

## Security posture (stated plainly — hardened repo, NOT regressed)
- **S11 confinement:** read_file + edit_file resolve every path through
  codebase.Confine and refuse `..`/absolute escapes; proven per-tool AND under the
  loop with an adversarial model. run() executes with the repo root as its working
  directory.
- **run injection-safety:** run reuses the existing runner primitive (`sh -c <cmd>`
  in the root) exactly as the healer already does — the model's command is passed as
  a single value; NO other untrusted input is interpolated into a shell template
  (the injection vector). Fork noted above (reuse-runner vs strict arg-vector).
- **Untouched:** MCP bearer-token/loopback auth, the config URL guard, the
  cost-attribution moat, the K4 verdict loop. The loop's Lens calls carry the
  X-Talyvor-Issue/-Workspace attribution (feature "agent-loop").
- **Egress:** nothing new leaves the machine — only the Lens model calls the loop
  makes (same gateway/trust boundary as every other agent call). No new service.

## Iterative-agent design (as built)
`internal/agentloop` — the model drives an OBSERVE/ACT loop over a confined tool set:
- **Tools** (`tools.go`): `search_codebase` (real semantic retrieval), `read_file`,
  `edit_file` (both S11-confined), `run` (runner primitive, rooted). `Registry`
  dispatch mirrors the MCP tool pattern. `done` is a loop sentinel.
- **Protocol** (`protocol.go`): one JSON object per turn `{thought,tool,args}`, parsed
  defensively (fences/prose salvage) — provider-agnostic, no client change.
- **Model seam** (`model.go`): `Model.Complete(messages)`; prod = lensModel, tests =
  scripted stubs.
- **Loop** (`loop.go`): system(tools) + user(task) → model picks a tool → dispatch →
  feed the observation back → re-plan. Terminates on `done`, `MaxSteps` (budget), or
  `MaxRepeat` no-progress (identical tool call recurs). Transcript trimmed to a cap.
  A failing `run` is just another observation → self-heal is native.
- **Wiring** (`cmd/agent/iterative.go` + `run --iterative`): retrieval-grounded,
  Lens-attributed, auto-applies confined edits, bounded by `--max-steps`.

## What's thin / deferred (honest)
- **Extension NOT mirrored.** CLI-first was the right call for depth; the VS Code
  extension's AgentMode still uses the single-pass pipeline. Mirroring the loop into
  the extension is a follow-on run. (Extension/JetBrains byte-unchanged this run.)
- **Loop is opt-in (`--iterative`); single-pass remains the default.** Flip is a
  one-liner (move legacy behind `--single-pass`, repoint ~10 run tests) — deferred to
  keep the unattended run non-breaking. See the design fork above.
- **Structured-JSON tool transport, not native tool-calling.** Robust + testable;
  native tool-calling (per-provider client change) is the documented alternative.
- **run() uses `sh -c`** (reusing the runner) — shell features work; strict
  arg-vector is the safer-but-more-limited alternative (fork above).
- **No transcript compaction/summarization** yet — just a trim cap; long tasks near
  the cap lose the oldest turns. Summarize-older is the next refinement.
- **Determinism:** every loop test uses a scripted `Model` + seeded tools (no live
  model); the observe/re-plan + self-heal tests run a REAL `go test` in a temp module.

## CI (this run)
Agent gate GREEN: `gofmt` clean, `go vet` clean, `go test -race` — 17 packages, 0
fail (existing single-pass + all new agentloop/CLI tests). gitleaks clean.
Extension/JetBrains byte-unchanged (only `agent/` + BUILD_STATE.md changed).

---

# Run: production-grade codebase index (branch `code-index-production`, off 22d4cf8)

Makes the semantic index incremental + honest + robust. Go/CLI only (the VS Code
layer is a later supervised run). TDD, red-first, `-pure` seam.

## VERIFY (state on 22d4cf8, read before building)
- `SemanticIndex{Root,EmbedModel,Chunks,Vectors}` — NO version, NO per-file hashes.
- `BuildFromRoot` re-embeds EVERY chunk every run. `Save` = direct os.WriteFile (not
  atomic). `LoadIndex` = json.Unmarshal (no version check). `Retrieve` = real cosine.
- MCP `search_codebase` (server.go:768-780) ranks via `FindRelevantFiles`
  (path-substring) + a FABRICATED score `rel := 1.0 - float64(i)*0.1` (line 773);
  `ask_code` auto-discovery uses the same path-substring. Server holds lensClient+root.
- The walk did NOT skip `.talyvor` → a re-index would ingest its OWN cache.

## Design forks taken (conservative + reversible — see each site)
- **content hash = SHA-256** (stdlib, collision-safe) vs a faster non-crypto hash
  (dep + collision risk on a correctness-critical skip). semindex_build.go.
- **`index` is INCREMENTAL by default**, `--full` forces a re-embed. A version/parse
  error on the existing index → loud full rebuild, never a silent mis-parse.
- **embed-model change ⇒ full rebuild** (mixing models corrupts cosine): reuse only
  when prev.Version==IndexVersion AND prev.EmbedModel matches.

## Phase narrative
- **Phase 1 — incremental re-indexing** (committed). SemanticIndex gains `Version` +
  `FileHashes`; `BuildIncremental` reuses unchanged files' chunks+vectors, embeds ONLY
  new/changed files, drops deleted files. `.talyvor` now skipped by the walk. `index`
  loads the prior index and re-indexes incrementally (reports re-embedded vs reused).
  RED→GREEN: a counting embedder proves a re-index after changing ONE file embeds only
  that file; a deleted file's chunks leave the index; a new file is embedded; confinement
  intact.
- **Phase 2 — staleness detection** (committed, branch pushed). New
  `codebase.Staleness(root, idx, maxFiles) → {Stale, Changed, Deleted}` re-hashes the
  working tree (walk + SHA-256, NO embedding — the cheap half of indexing) and diffs
  against the index's stored `FileHashes`. Wired warn-only into `loadRetriever`, so all
  three retrieval consumers (chat/ask/agent) surface a one-line `! codebase index is
  STALE (N changed, M deleted)` on stderr when the tree has drifted — retrieval still
  proceeds against the (stale) index rather than failing. RED→GREEN: stale after an edit
  (names the changed file, not the unchanged one), a deleted file lands in `Deleted`,
  fresh again after an incremental re-index, nil-index is a clean no-op; a consumer-side
  test proves `loadRetriever` warns on a drifted tree and stays silent on a fresh one.
  - **Fork — warn-only vs auto-refresh**: chose WARN-ONLY. A serving command
    (ask/chat/agent) must never silently spend Lens embed calls to rebuild the index;
    the user re-runs `talyvor-code index` (itself now incremental → cheap). Auto-refresh
    would be the reverse default and is a one-call change at the warn site if wanted.
  - **Fork — content-hash vs mtime staleness**: chose CONTENT-HASH (reads + hashes every
    embeddable file). Reliable — never a false "fresh" after a git checkout/touch that a
    bare mtime check would miss. Cost is one confined walk per serving invocation (bounded
    by the same skip-dirs as indexing, no network). A mtime+size fast-path is the
    documented future optimization; it needs the index schema to also store per-file
    mtime/size, so it is deferred rather than done half-way.
  - Security posture unchanged: the staleness walk goes through the SAME `walkRepoFiles`
    → `Confine` (S11) path as indexing; it only reads inside the repo root and sends
    nothing over the network (pure local hash compare). The index artifact stays local.
- **Phase 3 — MCP rewire (kill the fabricated score)** (committed). `search_codebase`
  and `ask_code` auto-discovery no longer rank via the path-substring
  `FindRelevantFiles` + the invented `rel := 1.0 - i*0.1` linear decay. Both now load
  the persisted SEMANTIC index (`LoadIndex(IndexPath(root))`), embed the query through
  Lens, and rank by true cosine — the exact `BoundIndex` retriever chat/agent use. New
  `search_codebase` payload is `{results:[{path,language,start_line,end_line,score}],
  chunks_indexed}` where `score` is real cosine. No index → an HONEST
  `{indexed:false, reason:"…run `talyvor-code index` first"}` (never a fake score, never
  a silent empty `files` that reads as "no matches"); Lens unconfigured →
  `{configured:false,…}`. RED→GREEN: a seeded on-disk index + a mocked query-embed prove
  the returned scores are the true cosines 0.8/0.6 (values the old 1.0/0.9 decay could
  never yield), that the no-index path is honest, and that ask_code auto-discovery ranks
  the semantically-closest file first. Grep proof: the linear-decay expression and every
  `FindRelevantFiles` CALL are gone from `internal/mcp` (the name survives only in a
  doc-comment naming what was replaced).
  - **AUTH UNTOUCHED (verified by diff)**: the rewire changed only the two tools'
    RELEVANCE source. Bearer-token check, `authOK`, `GenerateToken`, and
    `confinedReadPath` (S11) are byte-identical — `git diff` shows no auth/confinement
    lines changed. The path-substring `CodebaseIndex` + its reindex goroutine remain
    (still used by `get_codebase_summary`); nothing was orphaned.
  - **Fork — new `results` shape vs preserving `files`**: chose to RENAME the payload
    key (`files`→`results`) and swap `relevance`→`score`, because the value's MEANING
    changed from a fabricated per-file rank to a real per-chunk cosine; keeping the old
    key would let a client keep trusting a differently-defined number. A shape change is
    the honest signal. Reversible: re-add a `files` alias if a consumer needs it.
  - **Fork — mcpEmbedder duplication**: a 6-line Lens→Embedder adapter now exists in
    both `cmd/agent` (lensEmbedder) and `internal/mcp` (mcpEmbedder). Chose local
    duplication over hoisting a shared adapter into `lens`/`codebase`, to keep the phase
    self-contained and avoid coupling `codebase`↔`lens`. Documented for a future DRY pass.
- **Phase 4 — index robustness** (committed). Two hardening changes to the persisted
  artifact:
  - **Atomic Save (temp-then-rename)**: `Save` now writes the JSON to a
    `.codebase-index-*.tmp` beside the target (same dir → same filesystem), `Sync`s,
    `Close`s, then `os.Rename`s over the target; the temp is removed on any error path.
    A serving command that Loads while `index` rewrites sees the old or new WHOLE file,
    never a torn one. The temp lives inside `.talyvor` (already walk-skipped) so it is
    never itself indexed.
  - **Version gate in LoadIndex**: a persisted `version` != `IndexVersion` now fails
    LOUD (`index version N != supported M — run `talyvor-code index` to rebuild`) instead
    of `json.Unmarshal` silently mis-reading an evolved format. Legacy unversioned
    artifacts (no `version` field → 0) are rejected the same way. Every caller already
    treats a Load error as "no usable index": the `index` command rebuilds loudly,
    chat/ask/agent degrade to no-retrieval, MCP returns its honest "run index" message —
    so the gate is fail-safe everywhere.
  - RED→GREEN: a concurrent writers-vs-readers stress test (2000-chunk payload, 120
    rewrites, 4 readers) caught a truncated `unexpected end of JSON input` on the old
    `os.WriteFile` Save and is clean after temp-then-rename; a no-leftover-temp test
    guards the cleanup; version-mismatch and legacy-unversioned loads now error naming
    `version`. Full module `-race`: 17 packages ok, gofmt/vet clean.
