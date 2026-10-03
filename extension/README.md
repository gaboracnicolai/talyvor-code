# Talyvor Code

AI coding assistance in VS Code, with every request billed through **Talyvor Lens** and attributed
to a **Talyvor Track** issue — so the AI spend on a piece of work is a number you can look up
rather than a line on a provider invoice.

Extension id: **`talyvor.talyvor-code`**

## Before it does anything: you need a Lens

This extension is a client. It has no built-in model and no default account, and every AI feature
below calls **your** Lens instance — self-hosted or hosted — with **your** key. With `talyvor.lensUrl`
and `talyvor.lensApiKey` unset, the commands report *"Talyvor is not configured"* and stop. The
default `talyvor.lensUrl` is `http://localhost:8080`, which is a local Lens, not a service run for
you.

Run **`Talyvor: Test Lens Connection`** first. It is the one command that tells you whether the rest
will work. It makes two probes: `/healthz`, which says the URL and the network are good, and
`/v1/auth/me`, which says your API key is accepted. ⚠ It used to make only the first — and `/healthz`
takes no credential, so a wrong, revoked or expired key produced `✅ Connected to Lens v…` and then
every AI call failed. If Lens answers the key probe with anything other than 401 or 403 the command
reports what it always reported and claims nothing about the key, so an older Lens without that route
still reads as connected.

## Requirements

VS Code **1.85** or newer. No runtime dependencies are installed with the extension.

## What it does

| | |
| --- | --- |
| **Inline completions** | Ghost-text completions as you type, through Lens (`talyvor.enableCompletions`, on by default). |
| **Chat, explain, fix, refactor, tests** | `Talyvor: Open AI Chat` (⌘/Ctrl+Shift+L), plus explain / fix-error / refactor / generate-tests on the editor context menu. |
| **An agent that edits files** | `Talyvor: Start Agent Task` (⌘/Ctrl+Shift+A) plans, edits and runs commands inside the workspace root. |
| **Issue attribution** | Set an active Track issue (`Talyvor: Set Active Issue`) and every call is tagged with it; `Talyvor: Show AI Cost Dashboard` reads the spend back. |
| **Docs** | Hover, search and ask against a Talyvor Docs space (`talyvor.docsUrl` — optional; without it these commands are inert). |
| **PR review** | `Talyvor: Review Current PR` / `Review Selected Code`, using a GitHub token you supply. |
| **Claude Code, metered** | `Talyvor: Run Claude Code (metered by Lens)` opens a terminal running Claude Code through the Talyvor Code CLI, so its spend is billed by your Lens and attributed to the active issue. Needs the CLI — see below. |
| **Semantic index** | `Talyvor: Build Semantic Index` builds a local index the agent retrieves from. It stays on your machine. |

Model is picked per workspace from `talyvor.model` — the enumerated set is `claude-haiku-4-5`,
`claude-sonnet-4-6`, `claude-opus-4-6`, `gpt-4o`, `gpt-4o-mini`, `mistral-large`. Whether a given one
answers depends on which provider keys your Lens workspace has.

## What the agent is allowed to do to your machine

The agent's `run` tool executes model-authored strings through `sh -c` (`powershell -Command` on
Windows). That is bounded, and the bound is worth stating plainly because it is the difference
between a coding assistant and a remote shell:

- Commands are **parsed against an allowlist**, not prefix-matched. Anything the parser cannot read
  is **refused**, and a refusal is never downgraded to a prompt.
- Anything outside the allowlist needs an **explicit confirmation** from you.
- **With no interactive surface, the answer is refuse** — an unattended loop never auto-approves.
- It runs with the **workspace root as its working directory**, under a hard timeout, and file
  writes are confined to that root.

The `agentIterative` setting (**off** by default) switches the agent to a loop that applies edits
straight to disk with no per-file approval. Review those with `git`.

## Secrets

Keys pasted into `talyvor.lensApiKey`, `talyvor.trackApiKey`, `talyvor.docsApiKey` and
`talyvor.githubToken` are moved into the **OS keychain** (VS Code `SecretStorage`) on activation and
cleared from your settings file. Those settings exist to migrate an existing value, and are marked
deprecated for that reason.

## Metering Claude Code

Claude Code is metered by the **Talyvor Code CLI**, a separate binary, and
`Talyvor: Run Claude Code (metered by Lens)` is how you start it from VS Code: it opens a terminal
running `talyvor-code exec -- claude` in your workspace, handing the CLI your Lens URL and key through
the terminal's environment (never its command line). The CLI puts a loopback proxy between Claude Code
and your Lens, so every request is billed by Lens and attributed to the active issue — or to the issue
named by your branch. Your claude.ai login and connectors are untouched.

Install the CLI from the repository's releases:

```
curl -sSL https://raw.githubusercontent.com/gaboracnicolai/talyvor-code/main/install.sh | bash
```

Without it on your `PATH` the command says so and links here. **Claude Code is the one agent this
meters.** Cursor, Aider and Codex are not supported.

The extension bundles no model, no telemetry and no runtime dependencies: the packaged `.vsix` is
compiled JavaScript, a licence, and this page.

## Install

From the Marketplace, by the id this package publishes under (`publisher` + `name` in
`extension/package.json`):

```
code --install-extension talyvor.talyvor-code
```

To install a locally built package instead — the same artefact CI builds on every pull request:

```
cd extension && npm install && npm run package
code --install-extension talyvor-code-*.vsix
```

## Licence

BUSL-1.1 — see the licence tab. Source, issues and the full documentation:
<https://github.com/gaboracnicolai/talyvor-code>.
