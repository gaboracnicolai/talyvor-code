# Talyvor Code

**AI coding assistant powered by Talyvor Lens — every AI call attributed to your active Track issue.**

Three surfaces ship from this repository:

| Surface | Path | What it gives you |
| --- | --- | --- |
| VS Code extension (TypeScript) | `extension/` | Inline completions, chat, test generation, agentic mode, docs hover, status bar, dashboard |
| CLI agent (Go) | `agent/` | `ask`, `chat`, `test`, `run`, `review`, `commit`, `docs`, `serve` (MCP), `pr`, `shell`, `context`, `scope` |
| JetBrains plugin (Kotlin) | `jetbrains/` | Explain code · Generate tests · AI chat tool window (Phase 1 — feature parity coming) |

## Why Talyvor Code?

| Feature | GitHub Copilot | Cursor | Talyvor Code |
| --- | --- | --- | --- |
| Inline completions | ✅ | ✅ | ✅ |
| Chat panel | ✅ | ✅ | ✅ |
| Test generation | ✅ | ✅ | ✅ |
| Agentic mode | ⚠️ | ✅ | ✅ |
| **Cost per issue** | ❌ | ❌ | ✅ |
| **Self-hosted** | ❌ | ❌ | ✅ |
| **Any LLM provider** | ❌ | ⚠️ | ✅ |
| **Track integration** | ❌ | ❌ | ✅ |
| **Docs integration** | ❌ | ❌ | ✅ |
| **MCP server** | ❌ | ❌ | ✅ |
| **CLI agent** | ❌ | ❌ | ✅ |
| **Code review** | ❌ | ❌ | ✅ |
| **AI commit messages** | ❌ | ❌ | ✅ |
| **JetBrains IDE support** | ✅ | ❌ | ✅ (Phase 1) |

## VS Code extension

### Install (local dev)

```bash
cd extension
npm install
npm run compile
```

Open the `extension/` folder in VS Code and press F5 to launch a development host. To produce a distributable `.vsix`:

```bash
cd extension
npx vsce package
# → talyvor-code-0.1.0.vsix — install via "Extensions: Install from VSIX…"
```

### Configure

Open VS Code settings (⌘/Ctrl + `,`) and search for **talyvor**:

| Setting | Purpose |
| --- | --- |
| `talyvor.lensUrl` | Your Lens URL (e.g. `https://lens.talyvor.com`, or `http://localhost:8080` for local dev — remote URLs must be https) |
| `talyvor.lensApiKey` | Lens API key |
| `talyvor.trackUrl` | (optional) Track base URL for issue lookup |
| `talyvor.trackApiKey` | (optional) Track API key |
| `talyvor.docsUrl` | (optional) Talyvor Docs URL — enables hover + panel |
| `talyvor.docsApiKey` | (optional) Talyvor Docs API key |
| `talyvor.workspaceId` | Your Talyvor workspace ID |
| `talyvor.activeIssue` | e.g. `ENG-42` — settable via Command Palette |
| `talyvor.model` | Default model (haiku, sonnet, gpt-4o, ...) |
| `talyvor.enableCompletions` | Toggle inline completions |

### Commands

| Command | Shortcut | Description |
| --- | --- | --- |
| Open Chat | ⌘/Ctrl + Shift + L | AI chat panel with code-block insert |
| Explain Code | ⌘/Ctrl + Shift + E | Explain the selection |
| Fix Error | ⌘/Ctrl + Shift + F1 | Fix the diagnostic under the cursor |
| Generate Tests | ⌘/Ctrl + Shift + T | Generate tests for selection or file |
| Start Agent | ⌘/Ctrl + Shift + A | Multi-file agentic task |
| Search Docs | ⌘/Ctrl + Shift + D | Search Talyvor Docs inline |
| Set Active Issue | Palette | QuickPick across recent Track issues |
| Show Cost Dashboard | Palette | Per-issue, per-feature spend breakdown |
| Run Agent from Issue | Palette | Seed an agentic task from the active issue |
| Ask Docs | Palette / context menu | Q&A grounded in your docs |

The status-bar item cycles through `$(warning) Talyvor: Setup required` → `$(sparkle) Talyvor | $0.00` → `$(sparkle) ENG-42 | $0.03` and triggers a Track cost sync every 5 minutes.

## CLI agent

### Install

```bash
curl -sSL https://raw.githubusercontent.com/gaboracnicolai/talyvor-code/main/install.sh | bash
```

Or build from source:

```bash
cd agent && go install ./cmd/agent
```

### Configure

```bash
export TALYVOR_LENS_URL=https://lens.talyvor.com  # or http://localhost:8080 for local dev (remote URLs must be https)
export TALYVOR_LENS_API_KEY=tlv_...
export TALYVOR_WORKSPACE_ID=ws-1
export TALYVOR_ISSUE=ENG-42
# optional:
export TALYVOR_TRACK_URL=http://your-track:3000
export TALYVOR_TRACK_API_KEY=tlv_track_...
export TALYVOR_DOCS_URL=http://your-docs:4000
export TALYVOR_DOCS_API_KEY=tlv_docs_...
```

A reference file lives at [`.env.example`](.env.example).

### Commands

```bash
# Ask a question about code (one-shot)
talyvor-code ask "What does this function do?"

# Interactive chat REPL with /issue, /clear, /model
talyvor-code chat --issue ENG-42

# Generate tests for a source file
talyvor-code test src/auth.go

# Run an agentic multi-file task with --dry-run / --yes / --issue
talyvor-code run --dry-run "Add error handling to all API endpoints"

# Code review (general | security | performance) — staged diff by default
talyvor-code review --type security src/

# Conventional commit message from staged changes
talyvor-code commit --issue ENG-42 --push

# Search / ask / fetch Talyvor Docs
talyvor-code docs search "authentication flow"
talyvor-code docs ask   "How do we handle JWT refresh tokens?"
talyvor-code docs get   space-eng/page-abc

# Start the MCP server (binds 127.0.0.1:7777 by default)
# A bearer token is required: set TALYVOR_MCP_TOKEN, or one is
# generated and printed to stderr on start. Use --host 0.0.0.0
# only for deliberate LAN exposure (token still required).
TALYVOR_MCP_TOKEN=$(openssl rand -hex 32) talyvor-code serve --port 7777 --root .
```

### CLI flags

| Env var | Flag |
| --- | --- |
| `TALYVOR_LENS_URL` | `--lens-url` |
| `TALYVOR_LENS_API_KEY` | `--lens-key` |
| `TALYVOR_TRACK_URL` | `--track-url` |
| `TALYVOR_TRACK_API_KEY` | `--track-key` |
| `TALYVOR_DOCS_URL` | `--docs-url` |
| `TALYVOR_DOCS_API_KEY` | `--docs-key` |
| `TALYVOR_WORKSPACE_ID` | `--workspace` |
| `TALYVOR_ISSUE` | `--issue` |
| `TALYVOR_MODEL` | `--model` |

## JetBrains plugin (IntelliJ IDEA, GoLand, PyCharm, …)

```bash
cd jetbrains
gradle wrapper        # one-time, materialises ./gradlew + jar
./gradlew buildPlugin # → build/distributions/talyvor-code-0.1.0.zip
```

Install via **Settings → Plugins → ⚙️ → Install Plugin from Disk…** and configure under **Settings → Tools → Talyvor Code**.

Phase 1 ships:

- Right-click → **Talyvor → Explain Code** (Lens-routed, issue-attributed)
- Right-click → **Talyvor → Generate Tests** (auto-upgrades Haiku → Sonnet)
- **Talyvor Code** tool window with multi-turn chat

Roadmap: inline completions, streaming chat, full agent mode, Track + Docs integration parity. See [`jetbrains/README.md`](jetbrains/README.md) for the full plugin-side write-up.

## MCP integration

Talyvor Code ships an MCP server (`talyvor-code serve`) so Claude Code, Cursor agents, and other MCP-aware clients can plug into your coding context. The server binds `127.0.0.1` by default and requires a bearer token on every request — set `TALYVOR_MCP_TOKEN` to a stable secret (or copy the token printed to stderr on start). Add to your client's MCP config, sending the token in the `Authorization` header:

```json
{
  "mcpServers": {
    "talyvor-code": {
      "url": "http://localhost:7777/mcp",
      "headers": {
        "Authorization": "Bearer ${TALYVOR_MCP_TOKEN}"
      }
    }
  }
}
```

> Bind stays on loopback so an SSH tunnel (which forwards to `127.0.0.1`) works out of the box. `--host 0.0.0.0` exposes the server to other machines on the network — only do so deliberately; the bearer token is required either way and a warning is logged.

**Available tools:**

| Tool | What it does |
| --- | --- |
| `ask_code` | Question about the codebase, grounded in files you pick |
| `generate_tests` | Generate unit tests for a file |
| `review_code` | Markdown review with critical/warning counts |
| `get_active_issue` | Fetch the active Track issue |
| `search_codebase` | Path/filename relevance search |
| `read_file` | Read a file (with optional `lines: "10-50"`) |
| `search_docs` | Full-text + semantic search in Talyvor Docs |
| `ask_docs` | Docs Q&A with sources |
| `get_codebase_summary` | Languages, file/line counts, branch, repo |
| `generate_commit_message` | Conventional commit from a staged diff |

Tools degrade gracefully when their backing service is unconfigured — `get_active_issue` returns `{"configured": false, "reason": "track not configured"}` instead of erroring.

## Architecture

```
┌──────────────────┐   ┌────────────────────────┐   ┌─────────────────┐
│  VS Code         │   │  CLI agent              │   │  MCP clients     │
│  extension       │   │  talyvor-code           │   │  (Claude Code…)  │
└────────┬─────────┘   └────────────┬────────────┘   └────────┬─────────┘
         │                          │                         │
         │                          │       /mcp + /mcp/sse   │
         │                          └──────────────►──────────┘
         │                          │
         ▼                          ▼
┌───────────────────────────────────────────────────────────────────────┐
│  Talyvor Lens  ◄── all AI calls (X-Talyvor-Issue header)             │
│  Talyvor Track ◄── active issue lookup + cost sync + agent comments  │
│  Talyvor Docs  ◄── search / ask / page lookup                        │
└───────────────────────────────────────────────────────────────────────┘
```

## Gates

```bash
cd extension && npm run compile && npx tsc --noEmit
cd agent && go vet ./... && go test -race -count=1 ./...
```

Both gates run on every PR via [`.github/workflows/ci.yaml`](.github/workflows/ci.yaml). Tagged builds (`v*`) trigger the release job, which cross-compiles `talyvor-code` for `darwin/arm64`, `darwin/amd64`, `linux/amd64`, and `windows/amd64`, then attaches the binaries to a GitHub release.

## The moat

Every AI call carries an `X-Talyvor-Issue` header. Lens credits the spend to that issue; Track surfaces the running total on the issue page. When you implement `ENG-42`, every completion, every test, every agentic task, every code review — all of it — gets rolled up to that issue.

No other coding assistant does this.
