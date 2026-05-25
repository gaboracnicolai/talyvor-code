# Talyvor Code

**AI coding assistant powered by Talyvor Lens — every AI call attributed to your active Track issue.**

Two surfaces ship from this repository:

| Surface | Path | Status |
| ------- | ---- | ------ |
| VS Code extension (TypeScript) | `extension/` | Phase 1: scaffold + Lens/Track clients + status bar |
| CLI agent (Go) | `agent/` | Phase 1: command shell + `check`/`version` |

Phase 1 wires the connective tissue. Phase 2 ships completions + chat.

## Why Talyvor Code?

Every AI call carries an `X-Talyvor-Issue` header. Lens credits the
spend to that issue; Track surfaces the running total on the issue
page. No more guessing whether a single feature cost $0.50 or $50 to
ship — the answer's right there.

## VS Code extension

### Install (local dev)

```bash
cd extension
npm install
npm run compile
```

Open the `extension/` folder in VS Code and press F5 to launch a
development host.

### Configure

Open VS Code settings (⌘/Ctrl + ,) and search for "talyvor":

| Setting | Purpose |
| ------- | ------- |
| `talyvor.lensUrl` | e.g. `http://localhost:8080` |
| `talyvor.lensApiKey` | Lens API key |
| `talyvor.trackUrl` | (optional) Track base URL for issue lookup |
| `talyvor.trackApiKey` | (optional) Track API key |
| `talyvor.workspaceId` | Your Talyvor workspace ID |
| `talyvor.activeIssue` | e.g. `ENG-42` — settable via Command Palette |
| `talyvor.model` | Default model (haiku, sonnet, gpt-4o, ...) |

### Commands

- **Talyvor: Test Lens Connection** — pings `/healthz` and reports the version.
- **Talyvor: Set Active Issue** — quick input that validates against Track if reachable.
- **Talyvor: Show AI Cost Dashboard** — webview showing the active issue's cumulative spend.

The status-bar item shows the current state:
`$(warning) Talyvor: Not configured` → `$(sparkle) Talyvor | No issue` → `$(sparkle) Talyvor | ENG-42`.

## CLI agent

```bash
cd agent
make build              # → bin/talyvor-code
./bin/talyvor-code check --workspace ws-1 --lens-url http://localhost:8080 --lens-key tlv_xxx
```

Configuration also reads from env vars:

| Env var | Flag |
| ------- | ---- |
| `TALYVOR_LENS_URL` | `--lens-url` |
| `TALYVOR_LENS_API_KEY` | `--lens-key` |
| `TALYVOR_TRACK_URL` | `--track-url` |
| `TALYVOR_TRACK_API_KEY` | `--track-key` |
| `TALYVOR_WORKSPACE_ID` | `--workspace` |
| `TALYVOR_ISSUE` | `--issue` |

`ask` and `chat` land in Phase 2.

## Gates

```bash
cd extension && npm run compile && npx tsc --noEmit
cd agent && go vet ./... && go test -race -count=1 ./...
```

Both gates run on every PR via `.github/workflows/ci.yaml`.
