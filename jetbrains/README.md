# Talyvor Code for JetBrains

AI coding assistant for IntelliJ IDEA, GoLand, PyCharm, and the
rest of the JetBrains lineup. Powered by Talyvor Lens — every
AI call gets attributed to the active Track issue, just like the
VS Code extension and the CLI agent.

## Requirements

- JDK 17 (Temurin or any OpenJDK 17 build)
- Gradle 8.7+ (or use IntelliJ's bundled Gradle by opening this
  directory as a project)

The repository **does not** ship `gradle-wrapper.jar` — it's a
binary. Generate it once with a system Gradle:

```bash
cd jetbrains
gradle wrapper        # one-time, materialises ./gradlew + jar
```

After that, all subsequent commands work via the wrapper.

## Build

```bash
cd jetbrains
gradle buildPlugin    # → build/distributions/talyvor-code-0.1.0.zip
```

(or `./gradlew buildPlugin` once you've materialised the wrapper)

## Install

In your JetBrains IDE:
1. Settings → Plugins → ⚙️ → **Install Plugin from Disk…**
2. Select `jetbrains/build/distributions/talyvor-code-0.1.0.zip`
3. Restart the IDE when prompted

## Configure

**Settings → Tools → Talyvor Code:**

| Field | Notes |
| --- | --- |
| Lens URL | e.g. `http://localhost:8080` |
| Lens API key | Your `tlv_…` key |
| Workspace ID | The workspace this IDE belongs to |
| Active issue | e.g. `ENG-42` — every call gets attributed |
| Model | Defaults to `claude-haiku-4-5`; pick any Lens-supported model |

## Features

| Surface | Action |
| --- | --- |
| Right-click in editor → Talyvor → **Explain Code** | Sends the selection to Lens with the `explain` feature tag |
| Right-click in editor → Talyvor → **Generate Tests** | Language-aware prompt + framework detection + output sanitising; upgrades Haiku → Sonnet via the shared model catalogue |
| Right-click in editor → Talyvor → **Open Chat** | Reveals the `Talyvor Code` tool window |
| Tool window → composer | **Streaming** multi-turn chat with rolling history |
| Tools → Talyvor → **Test Lens Connection** | Fast `/healthz` reachability check |
| Tools → Talyvor → **Select AI Model** | Pick from the shared model catalogue |
| Tools → Talyvor → **Generate Shell Command** | NL → single command, with an advisory safety screen (display-only) |

See **[PLUGIN.md](PLUGIN.md)** for the full parity matrix, the
pure-core / IDE-shell architecture, the test suite, and the manual
verification steps for the UI surfaces.

## Roadmap

| Phase | Scope | Status |
| --- | --- | --- |
| 2 | Inline completions via the IntelliJ completion API | deferred (see PLUGIN.md / issues) |
| 3 | Streaming chat replies (mirrors the VS Code panel UX) | **done** |
| 4 | Full agent mode (multi-file tasks with diff review) | deferred |
| 5 | Track + Docs integration parity with the VS Code extension | deferred |

## Plugin metadata

| Key | Value |
| --- | --- |
| Plugin ID | `com.talyvor.code` |
| Plugin XML | `src/main/resources/META-INF/plugin.xml` |
| Since build | `241` (IntelliJ 2024.1) |
| Until build | `251.*` (IntelliJ 2025.1.x) |

## Verifying the build

```bash
cd jetbrains
gradle buildPlugin verifyPlugin
```

CI runs the same two tasks on every push — see
`.github/workflows/ci.yaml` `jetbrains` job.
