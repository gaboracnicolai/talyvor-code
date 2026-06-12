# Talyvor Code for JetBrains — status & parity

This document tracks what the JetBrains plugin actually does today,
how it is built, and how to verify the surfaces that can't be
unit-tested. It is the companion to `README.md` (install/configure)
and is kept current as the plugin advances toward VS Code parity.

The plugin began as a Phase-1 scaffold. The increments below moved it
toward functional parity with the `extension/` VS Code extension,
porting the extension's `*-pure` modules to dependency-free Kotlin
(unit-tested with JUnit) and wiring them into IDE surfaces.

## Architecture: pure core, thin IDE shell

The VS Code extension separates **pure** logic (no `vscode` import,
unit-tested) from VS Code-dependent glue. This plugin mirrors that:

| Pure Kotlin (JUnit-tested, no IntelliJ Platform dep) | Mirrors (VS Code) |
| --- | --- |
| `model/Models.kt` | `model/models-pure.ts` |
| `lens/SsePure.kt` | `lens/sse-pure.ts` |
| `testing/TestGenPure.kt` | `providers/test-generator-pure.ts` |
| `shell/ShellPure.kt` | `commands/shell-pure.ts` |

Keeping these in lock-step means the JetBrains, VS Code, and Go CLI
surfaces pick the same model defaults, parse the same SSE streams, name
test files the same way, and apply the same dangerous-command screen.
The JUnit suites are **direct ports** of the extension's tests, so a
divergence in behaviour fails a test on at least one side.

The IDE shell (`actions/`, `toolwindow/`, `*Settings`, `LensClient`)
depends on the IntelliJ Platform and is verified by compilation +
manual in-IDE checks (it can't be exercised without an IDE sandbox).

## What works today

| Surface | Entry point | Backed by | Verified by |
| --- | --- | --- | --- |
| Explain Code | Editor right-click → Talyvor | `LensClient.complete` | manual |
| Generate Tests (language-aware) | Editor right-click → Talyvor | `TestGenPure` + `Models` | unit + manual |
| Streaming chat | Tool window composer | `LensClient.completeStream` + `SsePure` | unit (parser) + manual |
| Open Chat | Editor right-click / tool window | — | manual |
| Generate Shell Command | Tools → Talyvor | `ShellPure` + `Models` | unit (helpers) + manual |
| Test Lens Connection | Tools → Talyvor | `LensClient.getStatus` | manual |
| Select AI Model | Tools → Talyvor | `Models` catalogue | manual |
| Settings (Lens URL/key, workspace, issue, model) | Settings → Tools → Talyvor Code | `TalyvorSettings` | manual |
| Per-issue cost attribution | every Lens call | `X-Talyvor-{Feature,Workspace,Issue}` headers | manual |

### Notable behaviours

- **Language-aware tests.** Generate Tests derives the language from the
  file extension (`TestGenPure.canonicalLanguageId`), sends the
  language-tailored system prompt + fenced payload, and sanitises the
  reply (strips `Here are the tests:` preambles and code fences) before
  showing it, titled with the detected framework.
- **Streaming chat.** The composer renders a live assistant bubble that
  grows per delta. It falls back to a single chunk when Lens returns
  plain JSON instead of SSE (some proxies ignore `stream:true`).
- **Model defaults never drift.** Generate Tests upgrades the cheap
  Haiku default to Sonnet via `Models.defaultForCommand("test-gen")`,
  the same mapping the VS Code + CLI surfaces use.
- **Shell safety is advisory.** `ShellPure.isCommandSafe` flips the
  result dialog to a warning for known-dangerous commands (`rm -rf /`,
  fork bombs, `curl … | sh`, …). The action is **display-only** — it
  never executes the command.

## Build & test

Requires JDK 17 and (first build only) a system Gradle to materialise
the wrapper — see `README.md`.

```bash
cd jetbrains
gradle wrapper --gradle-version=8.7   # one-time, generates ./gradlew
./gradlew test          # run the JUnit pure-logic suites
./gradlew test buildPlugin   # tests + the installable .zip (what CI runs)
```

CI (`.github/workflows/ci.yaml`, `jetbrains` job) runs
`./gradlew test buildPlugin` on every push/PR.

Current suite: **49 tests, 0 failures** — `ModelsTest` (6),
`SsePureTest` (15), `TestGenPureTest` (17), `ShellPureTest` (11).

## Manual verification (UI surfaces)

These need a running Lens + a sandbox IDE (`./gradlew runIde`) and a
configured Lens URL/key. Each step is the minimum to confirm the
surface works end-to-end.

1. **Test Lens Connection** — Tools → Talyvor → Test Lens Connection →
   expect `✅ Connected to Lens v…` (or a clear ❌ when the URL is
   wrong). Confirms `getStatus`/healthz.
2. **Select AI Model** — Tools → Talyvor → Select AI Model → pick a
   model → reopen Settings → Tools → Talyvor Code and confirm the Model
   field updated.
3. **Streaming chat** — open the Talyvor Code tool window, send a
   prompt, confirm the reply appears incrementally (not all at once) and
   that a follow-up message carries prior context.
4. **Generate Tests** — select a function in a `.go`/`.py`/`.ts` file →
   right-click → Talyvor → Generate Tests → confirm the dialog title
   shows the right framework (Go testing / pytest / Jest) and the body
   has no stray ``` fences or "Here are the tests:" preamble.
5. **Generate Shell Command** — Tools → Talyvor → Generate Shell
   Command → describe a task → confirm a single command (no fences). Try
   a destructive description and confirm the warning dialog appears.

## Not yet ported (deferred — see GitHub issues)

These exist in the VS Code extension and remain open work. They were
deferred tonight to keep each increment small, build-green, and (where
possible) unit-tested. Filed as issues rather than silently skipped:

- **Inline completions** (`registerInlineCompletionItemProvider`
  equivalent via IntelliJ's completion API).
- **Cost tracking + status bar + 5-min Track cost-sync** and the cost
  dashboard (`getCostForIssue` analytics).
- **Track integration**: set/show active issue, create issue from code.
- **Docs integration**: panel, hover provider, search/ask/link,
  spec-watcher.
- **Agent mode** (multi-file tasks + self-heal loop).
- **Rules / context / scope loaders** (`.talyvor-rules`,
  `.talyvor-context`, `.talyvor-scopes`).
- **Remaining editor actions**: Fix Error (diagnostics-aware), Refactor,
  Review Selection / Review PR, Generate Tests for File.
- **Settings parity**: expose `docsUrl`/`docsApiKey`/`githubToken`/
  `enableCompletions` in the settings UI (the state fields for the first
  two already exist; the latter two are not yet modelled).
