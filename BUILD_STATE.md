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

## Status — production-index run COMPLETE (awaiting review, NOT merged)
- All 4 capabilities landed on `code-index-production` (off `22d4cf8`): incremental
  re-index (`393fdb4`) · staleness (`cb584ba`) · MCP honest relevance (`966d89f`) ·
  atomic+versioned artifact (`49d049e`).
- **PR #21** open against `main`. CI ALL GREEN: agent (gofmt/vet/`-race`) ✓ · extension
  ✓ · gitleaks ✓ · jetbrains ✓ · semgrep ✓.
- Scope verified by diff: only `agent/` + this file. MCP auth / config guard / cost moat
  / K4 verdict loop / agentloop / extension / jetbrains / gitleaks untouched. No container
  created this run.
- STOPPING before merge per run rules (do not merge; end with a PR).
- **What's left (explicitly out of scope this run)**: VS Code + JetBrains retrieval
  wiring (later supervised run); the two documented micro-optimizations (mtime/size
  staleness fast-path needs a schema field; hoist the duplicated Lens→Embedder adapter);
  no completion-wiring, no agent-loop mirror, `--iterative` default still off.

---

# Run: VS Code extension → CLI parity (branch `code-extension-agent`, off `c1aa63f`)

SUPERVISED. Bring the merged iterative agent loop (`internal/agentloop`, #20) and the
production semantic index (incremental + honest MCP relevance, #21) INTO the extension,
which today still uses the single-pass pipeline. Do NOT merge — one PR, commit per phase.

## VERIFY (real state on c1aa63f, read before building — do NOT assume)
- **The extension is a fully independent TypeScript reimplementation.** It NEVER shells
  out to the Go `talyvor-code` binary (the only `child_process`/`spawn`/`execFile` uses
  are heal's build runner and `git` for PR/context). So "bring agentloop/index in" =
  PORT/MIRROR to TS, not shell-out. BUILD_STATE (#20) already recorded the intent:
  "Extension NOT mirrored … Mirroring the loop into the extension is a follow-on run."
  "Do not touch agentloop/index except to CALL them" ⇒ don't modify the Go packages
  (I work only under `extension/`); the extension mirrors their behavior in TS.
- **Current extension agent = single-pass** (`src/agent/AgentMode.ts`): `plan()` (one
  Lens call) → `executeOne()` per file (one blind Lens call each; reads the target file
  but NO sibling/retrieval grounding) → `awaiting_approval` → user approves → apply →
  optional `applyAndHeal` (build → ask model → apply fixes → retry ≤ MAX_HEAL_ATTEMPTS).
  No observe/re-plan tool loop. `agent-pure.ts` holds the vscode-free bits (parsePlan,
  state machine, unified-diff).
- **No semantic retrieval anywhere in the extension.** The TS Lens client
  (`src/lens/client.ts`) has complete/completeWithUsage/completeStream/getStatus/
  getCost — **no `embed()`**. "Context" today is `.talyvor-context` (user YAML) + line-
  prefix parsing for completions. No `.talyvor/codebase-index.json` load. So Phase 2
  must add `embed()` + a TS Retriever over the Go index JSON.
- **S11 already ported + tested**: `src/agent/confine-pure.ts` `absolutise()` mirrors the
  Go `confine`; `confine-pure.test.ts` exercises escapes. I reuse it for the loop's file
  tools.
- **Test seam — TWO conventions; only ONE gates CI:**
  - `test/*.test.ts` = self-contained runners (plain fns + `main()` + `process.exit(1)`
    on failure, NO `vscode` import). `npm test` → `out/test/runTest.js` spawns each
    compiled `out/test/*.test.js` in its own process. **THIS is what CI runs** (ci.yaml
    `extension` job: install → compile → `tsc --noEmit` → `npm test`). Baseline: 16/16
    files pass headlessly. → my pure-seam tests go HERE so they truly run in CI.
  - `src/**/*-pure.test.ts` = `node:test`, run only by `test:unit` — **CI does NOT run
    these**. (confine-pure.test.ts lives here.) I won't rely on this path for CI proof.
- **Go loop semantics to mirror exactly** (`internal/agentloop`): observe/act loop,
  `Config{MaxSteps 20, MaxRepeat 2, MaxTranscript 40}`, StopReason{done,budget,
  no-progress,error}; no-progress trips on the 3rd identical (tool+args) call; budget
  runs exactly MaxSteps on distinct calls; malformed reply fed back with a JSON hint
  (bounded → no-progress); tool error = observation (never kills the loop); done carries
  a summary; transcript trims to system + most-recent. Tool transport = one JSON object
  per turn (NOT native tool-calling) — mirror it.

## Plan (TDD the pure seam in `test/*.test.ts`; STOP-and-report on any UI wall)
1. **Phase 1 — pure iterative loop** (`src/agent/loop-pure.ts`): Message/Model/Tool/
   Registry/parseToolCall/Agent.run + StopReason/Config/Result. TDD all Go scenarios.
2. **Phase 2 — retrieval-grounding**: `embed()` on the TS Lens client + a TS Retriever
   over the Go index JSON (version gate, cosine, honest-absent). TDD with a fake embedder.
3. **Phase 3 — VS-Code-bound wiring**: real tools (read/edit via confine-pure+vscode.fs,
   run via child_process, search via Retriever) + an opt-in `AgentMode.startIterativeTask`
   behind a setting; DOCUMENT the manual-verify steps for the panel/editor surface.

## Phase narrative
- **Phase 1 — pure iterative loop** (`79ec661`). `src/agent/loop-pure.ts`: the vscode-
  free TS mirror of `internal/agentloop` — Message/Model/Tool/Registry seams, defensive
  `parseToolCall`, `Agent.run` (observe/act, step budget, no-progress detector,
  transcript trim, edited-file tracking, tool-error-as-observation). `test/agent-loop.
  test.ts` (16 assertions, self-contained runner → runs in CI) drives it with a scripted
  model stub + stub tools and mirrors the Go suite exactly: observe→re-plan (turn 2 sees
  the observation), budget at MaxSteps, no-progress at the 3rd identical call, edit→fail
  →edit aborts at step 5, garbage→no-progress, bad-format→JSON hint, model-error→
  StopError, unknown/throwing tool→observation, edited-files unique, transcript trim
  keeps system.
  - **Fork — run() never throws**: Go returns `(Result, error)`; TS folds a hard model
    error into `Result.error` with `stop=Error`, so the panel always gets a structured
    outcome. Reversible (add a throwing wrapper if wanted).
- **Phase 2 — retrieval-grounding** (this commit). `src/agent/retrieval-pure.ts`: loads
  the SAME on-disk `.talyvor/codebase-index.json` the CLI writes (exact Go json tags),
  version-gated (`parseIndex` fails loud on a mismatch / legacy unversioned), embeds the
  query via an `Embedder` seam, ranks by TRUE cosine (Go `cosine` mirrored), honest-
  absent (`loadRetriever` → null when the file is missing). Added `LensClient.embed()`
  (the OpenAI embeddings proxy, `code-embed` attribution, index-ordered vectors) — the
  TS client had no embed before. `test/retrieval.test.ts` (12 assertions, CI-run): real-
  cosine ranking (0.8/0.6, NOT the old fabricated 1.0/0.9 — the honest-MCP twin), version
  gate, absent→null, top-k, degenerate-safe cosine, and a fetch-stub proving embed hits
  `/v1/proxy/openai/v1/embeddings` and re-orders by `index`.
  - **Fork — index BUILD story (consume-only for now)**: the extension READS the CLI-
    built index and retrieves; it does NOT itself (re)build it this run. Conservative-
    reversible: adds NO new dependency and NO large port. When the index is absent,
    retrieval degrades honestly (null → "run `talyvor-code index`"). Two documented ways
    to add in-extension (incremental) building later, deferred for a decision: (a) shell
    out to the `talyvor-code` binary's `index` (calls the Go incremental indexer — new
    binary dependency + discovery), or (b) port the walker/chunker/atomic-save to TS
    (large, duplicates the Go codebase package). Retrieval itself needs neither — it
    works on any pre-built index. Flagged for the supervisor.
  - Security: only the query text is sent to Lens (feature `embed`), same trust boundary
    as chat; the index stays a LOCAL file, never uploaded. Load path uses node:fs on the
    confined `<root>/.talyvor/` only.
- **Phase 3a — real loop backends** (this commit; still headless-tested). `src/agent/
  loop-tools.ts`: the node-backed tools that make the pure loop actually work —
  `read_file`/`edit_file` (node:fs, S11-confined via `absolutise`), `run`
  (child_process `sh -c` / powershell, cwd-confined + timeout), `search_codebase` (the
  Retriever seam) + `defaultTools(root, ret)`; plus `lensLoopModel` adapting a Lens
  client to the loop's Model (feature `agent-loop`, forwards the full transcript incl.
  the system message — mirrors the CLI `iterative.go` adapter — and reports token usage
  for cost tracking). `test/loop-tools.test.ts` (9 assertions, CI-run, all vscode-free):
  read/edit confined + REFUSE `../` escapes, `run` captures exit+output and treats a
  non-zero exit as an observation (not a throw), search returns real chunks / honest
  note when absent, the adapter maps messages + tags + usage, and an INTEGRATION test
  drives the pure loop over the REAL tools (scripted model: read→edit→run→done) proving
  the edit hits disk and edited-files are tracked.
  - **Fork — tools operate on DISK (node:fs), not vscode.workspace.fs**: chosen so the
    whole tool set is headless-testable and matches the CLI loop's on-disk semantics
    (what a subsequent build/run sees). Trade-off: they don't reflect unsaved editor
    buffers — the bound wiring (Phase 3b) documents "save open editors first". Reversible
    (swap to vscode.fs if unsaved-buffer awareness is needed, at the cost of testability).
  - **Fork — `run` uses `sh -c` (non-literal spawn)**: the command IS the agent tool's
    intended payload (running the model's build/test command is the tool's purpose); no
    other input is interpolated into the shell string, cwd is the confined root, there's
    a timeout. Mirrors the CLI `internal/runner` (chose `sh -c` over arg-vector so
    pipes/&& work) and the existing heal.ts. Documented with a justified `// nosemgrep`
    on the dangerous-spawn-shell rule (the intent is explicit, unlike heal.ts's silent
    twin).
- **Phase 3b-i — iterative orchestration** (headless-tested). `src/agent/iterative-pure.ts`
  `runIterativeLoop(deps)`: the vscode-free glue — load the retriever (honest-absent →
  `indexed:false`), build the confined tools + the Lens model, run the loop, forward
  per-turn token usage. `test/iterative.test.ts` (3 assertions, CI-run, fake Lens + temp
  workspace): no-index → loop still runs + edit hits disk + no embed attempted; with-index
  → `indexed:true` + a search turn embeds the query exactly once; usage aggregates across
  turns. This is the seam the bound wrapper calls.
- **Phase 3b-ii — VS-Code-bound wiring** (BUILT; MANUAL-VERIFY ONLY — see below).
  `AgentMode.startIterativeTask` (thin wrapper over `runIterativeLoop`): task state +
  cost recording + a "Talyvor Agent — Iterative" output channel that replays the
  observe/act transcript + records applied edits so the completed view reports an
  accurate count + posts the Track completion comment. `AgentPanel.startTask` routes here
  when the new `talyvor.agentIterative` setting (default **false**) is on, else the
  untouched single-pass path. `package.json` gains the setting.
  - **Why manual-verify, not automated**: this surface imports `vscode` (OutputChannel,
    webview, `workspace.getConfiguration`, the panel). VS Code's API does not verify
    headlessly, and the run rules forbid a mock/fake-harness/skip-test. So it is NOT
    unit-tested — by design. Everything it *calls* (loop, tools, retriever, model
    adapter, orchestration) IS fully headless-tested (43 assertions across 4 CI-run
    files). The bound wrapper is deliberately thin: state assignment + emit + channel +
    the tested `runIterativeLoop`.
  - **UX fork (documented)**: the iterative agent applies edits DIRECTLY to disk (no
    per-file approval gate — mirrors the CLI `run --iterative`); the user reviews via
    git/editor. The webview shows planning→executing→completed/failed; the detailed
    turn-by-turn trace is in the output channel. Default OFF so the existing approve/apply
    flow is the unchanged default.

## MANUAL VERIFICATION (VS-Code-bound surface — a human must click)
The pure seam is proven in CI. The following require a running VS Code (Extension
Development Host) — I could not and did not automate them:
1. `npm run compile` in `extension/`, press F5 (Extension Development Host).
2. Set `talyvor.lensUrl` / `talyvor.lensApiKey` (+ `workspaceId`) to a reachable Lens.
3. Enable **Talyvor Code: Agent Iterative** (`talyvor.agentIterative`) in Settings.
4. Run **Talyvor: Start Agent Task** → describe a small change → observe:
   - the "Talyvor Agent — Iterative" output channel streams the loop's turns and the
     final `■ <stop> after N step(s)` + summary + edited files;
   - the webview ends in ✅ completed (or Failed with the stop reason);
   - the edits are on disk (check the editor / `git status`) — applied WITHOUT a per-file
     approval prompt (expected — review via git);
   - **save any open editors first** (tools write to disk via node:fs, not the vscode FS
     layer — unsaved buffers are not seen).
5. With NO `.talyvor/codebase-index.json` present: the channel notes "no semantic index …
   run `talyvor-code index`" and the loop still runs (search is note-only). Run
   `talyvor-code index` (CLI) to populate it, re-run, and confirm a `search_codebase`
   turn returns ranked chunks.
6. Toggle the setting OFF → **Start Agent Task** still drives the original single-pass
   plan→approve→apply flow (regression check).

---

# Run: in-extension TS index BUILDING (branch `code-extension-index-build`, off `f13a730`)

SUPERVISED. Completes the extension's retrieval story: today it only CONSUMES a CLI-built
index (retrieval-pure.ts, #22); after this it BUILDS its own in-process TypeScript index —
no shell-out to the Go binary (DECISION MADE: in-process = no PATH trust surface, no second
install, no version skew). Do NOT merge — one PR, commit per phase.

## VERIFY (Go source mirrored + extension seam, read on f13a730)
- **Go writer (internal/codebase) — the exact semantics to mirror:**
  - Walker `IndexDirectory`: skipDirs = {.git, .talyvor, node_modules, vendor, .next, dist,
    __pycache__} (NOTE: `build` is in the map as **false** → NOT skipped); skipSuffixes =
    {.min.js, .min.css, .lock, -lock.json}; maxFiles cap 500. `.talyvor` IS skipped (the
    already-fixed self-index bug — must NOT reintroduce).
  - `DetectLanguage`: ext→lang map; `embeddableLang(lang) = lang != "" && lang != "Other"`
    (only recognized languages embed).
  - `ChunkFile`: whitespace→none; Go→`chunkGo` (col-0 func/func(/type/const/var boundaries,
    each absorbs its preceding `//` block; header chunk before first decl; a decl > 160 lines
    is window-split; no decls → fall back to windows); non-Go / declless-Go → `windowRange`
    (50-line windows, 10-line overlap → next start = end-9). 1-based inclusive spans.
  - `BuildIncremental`: `walkRepoFiles` (IndexDirectory → embeddableLang filter → Confine
    (S11) → ReadFile 100KB cap → SHA-256 hex hash); reusable = prev!=nil && prev.Version==1 &&
    (prev.EmbedModel==""||==DefaultEmbedModel); group prev chunks+vecs by file; per entry stamp
    FileHashes[path]=hash, REUSE chunks+vecs when hash matches prev, else `ChunkFile`→newChunks;
    embed only newChunks (batch 64); Chunks = reused++new, Vectors = reusedVecs++newVecs.
  - `Save`: MkdirAll → marshal → CreateTemp in dir → Write → Sync → Close → **Rename** (atomic);
    temp removed on any error. `IndexVersion` = 1 stamped. `DefaultEmbedModel` =
    "text-embedding-3-small". `IndexPath` = <root>/.talyvor/codebase-index.json.
- **Extension reader (retrieval-pure.ts, #22) — the format the writer MUST produce:**
  `Chunk {file, language, start_line, end_line, content}` (snake_case), `SemanticIndex
  {version, root?, embed_model?, file_hashes?, chunks, vectors}`, `parseIndex` version-gates
  (version !== 1 → throw). **The writer IMPORTS these very types + `INDEX_VERSION` +
  `DEFAULT_EMBED_MODEL` + `indexPath` from retrieval-pure.ts → format agreement is guaranteed
  by construction, and I do NOT modify the reader.** confine-pure `absolutise` = S11.
- **Test seam**: CI runs `test/*.test.ts` (self-contained node runners), NOT `*-pure.test.ts`.
  Baseline 20/20 files green. New tests go in test/*.test.ts, 0 skips.

## Plan (TDD the pure seam; any vscode-bound wiring = manual-verify)
1. Walker + chunker + language (`index-build-pure.ts`): mirror skipDirs/suffixes/lang set +
   ChunkFile boundaries; .talyvor NOT indexed; S11 confined reads (adversarial path refused).
2. Incremental core: SHA-256 hash + reuse/embed-only-changed/drop-deleted (counting embedder).
3. Atomic + versioned Save (temp-then-rename; version stamp the reader accepts).
4. Round-trip linchpin: THIS writer → the EXISTING retrieval-pure reader → retrieve (real cosine).
5. Wire: opt-in "build index" command (vscode-bound → manual-verify, documented).

## Phase narrative (TS index BUILDER — all TDD, mutation-verified where impl preceded test)
- **Phase 1 — walker + chunker + language** (`21a5a10`, RED→GREEN): `index-build-pure.ts`
  mirrors DetectLanguage/embeddableLang, SHA-256 contentHash (proven vs the "abc" vector),
  ChunkFile (Go decl-aware + non-Go 50/10 windows), and walkRepo (Go skipDirs incl.
  `.talyvor` NOT reindexed; `build` NOT skipped; skipSuffixes; only embeddable langs). S11:
  every read via `readConfined` (absolutise) — refuses `../`; symlinks out of root NOT
  followed (isFile()/isDirectory() false for them) — a hardening over the Go walker.
  METHOD NOTE: the module is one cohesive mirror of one Go package, so buildIncremental +
  saveIndex landed here too; their behavior is proven by Phases 2–4, EACH mutation-verified.
- **Phase 2 — incremental** (`…`): counting-embedder proves edit-one→re-embed-only-that,
  delete→chunks+hash leave (0 embeds), add→embed-only-new. Mutation: `reusable=false` → the
  reuse/drop/add asserts go RED. (mirror of Go incremental_test.go)
- **Phase 3 — atomic + versioned Save**: version stamp accepted by the reader (round-load),
  whole-file replace, no temp leftover, and a **worker_threads** reader (real OS-thread
  parallelism) hammering the target during 150 rewrites of a 2000-chunk index NEVER sees a
  torn file. Mutation: direct writeFileSync (no temp-then-rename) → the concurrent reader
  catches a torn read (RED). This is the node equivalent of the Go goroutine race.
- **Phase 4 — writer↔reader round-trip (LINCHPIN)**: build with THIS writer → load with the
  EXISTING unmodified retrieval-pure reader → retrieve (auth chunk ranks first by real
  cosine); version + chunk-count + 1-based spans round-trip; on-disk fields are snake_case
  with file_hashes + embed_model; incremental output stays reader-valid. Mutation: bump the
  writer's version stamp → the reader rejects it (RED). Format agreement PROVEN end-to-end.
- **Phase 5 — refresh orchestration + opt-in command**: `refreshIndex` (vscode-free: load
  prior → incremental → atomic save → delta; a version-mismatched prior forces a LOUD full
  rebuild) + `indexDelta` are headless-tested (full→all-reused→edit-one→1-changed;
  version-mismatch→full-rebuild). The `talyvor.buildIndex` command (`index-command.ts`) is a
  THIN vscode wrapper (progress notification + Lens-backed embedder + result toast) —
  MANUAL-VERIFY ONLY (imports vscode; no mock/skip per the run rules).

## Security posture (held)
- **S11**: every walk/hash read goes through `readConfined`→`absolutise` (refuses `../`);
  symlinks out of root are not followed (proven adversarially). Reads stay inside the root.
- **Trust boundary unchanged**: only chunk text is sent to Lens (feature `embed`), exactly
  as chat does; the built index is a LOCAL file under `<root>/.talyvor/`, never uploaded.
- **Reader untouched**: the writer IMPORTS retrieval-pure's types/constants; I did not
  modify the #22 reader's format. Go packages / MCP auth / config guard / cost moat / K4
  verdict loop untouched (only `extension/` + BUILD_STATE changed — verified by diff).

## MANUAL VERIFICATION (vscode-bound command — a human must click)
1. `npm run compile` in `extension/`, press F5 (Extension Development Host).
2. Set `talyvor.lensUrl` / `talyvor.lensApiKey` (+ `workspaceId`) to a reachable Lens.
3. Run **Talyvor: Build Semantic Index** (`talyvor.buildIndex`):
   - a progress notification "building semantic index…" appears;
   - on success a toast reports "N chunks across M files — X re-embedded, Y reused →
     .talyvor/codebase-index.json"; the file exists under `<root>/.talyvor/`.
4. Run it AGAIN with no edits → the toast shows all files **reused**, 0 re-embedded
   (incremental). Edit one file, re-run → exactly 1 re-embedded.
5. Confirm the built index is consumed by retrieval: enable `talyvor.agentIterative` (#22),
   run an agent task, and confirm a `search_codebase` turn returns ranked chunks (writer→
   reader round-trip live). Deleting `.talyvor/` and re-running rebuilds from scratch.

---

# Run 7 — K4 mechanical verdicts for the ITERATIVE loop (branch `code-k4-iterative-verdicts`, off `a733c57`)

WATCHED / moat-integrity: a mis-attributed verdict corrupts K4 data. Extend K4 mechanical-
verdict reporting to `internal/agentloop` (the primary code path — today reports nothing).
Client-safe: calls the already-wired `POST /v1/output-verdicts/{id}/mechanical`; holds NO
authority; flag-gated on `ReportVerdicts` (default OFF); best-effort/never-blocks. NOT H5,
NOT money/provenance, NOT the GET reader, NOT PR/spec attribution.

## VERIFY (read on a733c57)
- **Proven heal-loop pattern** (`agent/cmd/agent/main.go:889-944`): tracks the LAST single
  repair generation's `output_id` (`usage.OutputID` from `lc.CompleteWithUsage`, i.e. the
  `X-Talyvor-Output-Id` header, `client.go:60,126`); reports it 1:1 for the NEXT build via
  `ReportMechanicalVerdict` (`client.go:130-160` → the endpoint); best-effort (report error
  logged + swallowed, never breaks the build); gated on `cfg.ReportVerdicts`. **It explicitly
  SKIPS the initial multi-file plan as "1:many, NOT soundly attributable → not reported."**
  `mechanicalVerdict(cmd, success)` (main.go:967) maps to tests_passed/tests_failed/compiled/
  compile_failed — it does NOT gate on whether the cmd is actually a build/test.
- **Iterative loop** (`agent/internal/agentloop/loop.go`): ONE tool call per turn (each
  `model.Complete` → one generation → one `parseToolCall` → one `Dispatch`). Tools:
  search_codebase/read_file/edit_file/run. The `run` tool (`tools.go:192-211`) executes an
  ARBITRARY command via `runner.Run` and returns the exit only inside the observation string
  `"$ <cmd>\nexit <N>\n<out>"`. The `Model` seam (`model.go:16`) returns `(string, error)` —
  **no output_id**; `iterative.go` `lensModel.Complete` calls `lc.Complete` which DISCARDS
  the output_id. Registry is `map[string]Tool`.
- **THE PAIRING CRUX**: a `run` tests the CUMULATIVE file state of ALL `edit_file` turns since
  the last run. If >1 edit preceded it, the outcome is 1:many — blaming any single generation
  (esp. a FAILURE) can fault an innocent edit → corrupts K4.

## STATED PAIRING RULE — "sound 1:1, or skip" (justified)
Track the `output_id`s of `edit_file` generations since the last build/test `run`. On a
build/test `run`, report the mechanical verdict for a generation ONLY when EXACTLY ONE
un-verdicted code-producing generation preceded it AND its output_id is known — a true 1:1.
Otherwise SKIP (report nothing): zero edits, or >1 edits (1:many batch), or an unknown/empty
output_id. A build/test run always CLEARS the pending set (tested — reported or skipped).
Non-build/test runs are IGNORED (don't report, don't clear). Build/test-ness is a CONSERVATIVE
`looksLikeBuildOrTest(cmd)` gate.
- **Justification**: mirrors the heal loop's OWN "skip the 1:many batch, report only the 1:1"
  discipline. Soundness > coverage because a mis-attributed verdict CORRUPTS the moat while a
  skipped one merely lowers coverage. The common `edit→run→edit→run` pattern is each a clean
  1:1 (full coverage); only batched `edit→edit→run` is skipped. Conservative build-gate: a
  false positive (verdict for `ls`) corrupts; a false negative (skip a real build) only lowers
  coverage.
- **Alternatives rejected**: "attribute to the most-recent edit" and "attribute to each edit in
  the batch" both mis-attribute a FAIL to innocent generations — rejected for moat integrity.

## Design (additive, behavior-preserving — agentloop was call-only)
- agentloop: optional `OutputIdentified{ LastOutputID() string }` (Model capability);
  `run` tool exposes `LastRun() (exit int, ran bool)`; an optional `Observer{ ObserveStep(
  StepInfo) }` + `Config.Observer` the loop calls each step (nil ⇒ no-op ⇒ byte-identical).
- cmd/agent: `*lensModel` uses `CompleteWithUsage` + implements `LastOutputID`; a
  `verdictObserver` (implements the rule, calls a `verdictReporter` iface the real
  `*lens.Client` satisfies) wired into `Config.Observer` ONLY when `cfg.ReportVerdicts`.

---

# Run: PR attribution — thread output_ids + caller (branch `code-attribution-caller`, off `b9b3c12`)

Client-side, no authority, flag-gated. Phase 1 = the prereq (a prior recon STOPPED here);
Phase 2 = the caller. STOP before merge.

## RE-VERIFIED recon facts (all still true on b9b3c12)
1. single-pass `generateChange` (main.go:1761, feature `agent-execute`) uses `lc.Complete`,
   which drops OutputID (client.go:44-50 returns only `out.Text`).
2. the ITERATIVE path captures ids (verdictObserver, verdict_observer.go:47) but
   `runIterativeAgent` returned at main.go:684 BEFORE Phase-4 PR creation.
3. `PRConfig` (pr.go:28) + `agentloop.Result` (loop.go:71) carried no ids.
4. ⇒ a client in the PR path had nothing to POST.

## Phase 1 — thread the ids + fix the --iterative --pr no-op
- **Survival gate = "SURVIVED INTO THE COMMITTED DIFF", not "touched during the run".**
  - Level 1 (loop, `agentloop.Result.EditAttribution` map file→last-writer output_id):
    edit_file writes COMPLETE content, so the last writer per file wins; an earlier
    generation whose write is overwritten is dropped. Proven: A superseded by B on foo.go
    → foo.go attributes to B only (attribution_test.go).
  - Level 2 (`survivingAttributions(editAttr, committedFiles)`, cmd/agent): keep only ids
    whose file is in `git diff base...branch` (gitpkg.GetChangedFiles). A file edited then
    reverted is absent from the committed diff → dropped. Distinct + sorted. Proven incl.
    revert-drops-all (attribution_test.go).
- **--iterative --pr FIX — chose MAKE IT WORK (not warn).** Why: Phase 2 attributes after a
  PR lands, and fact #2 says ONLY the iterative path captures ids — so the iterative path
  must create the PR or the whole feature is inert; a warn would leave nothing to attribute.
  `runIterativeAgent` now returns `(agentloop.Result, error)`; the run command's iterative
  branch opens a PR (reusing `runPRAfterAgent`) when `--pr` + edits exist. Default (no --pr)
  is byte-identical.
