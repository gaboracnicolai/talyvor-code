# Talyvor Code for JetBrains

AI coding assistant for IntelliJ IDEA, GoLand, PyCharm, and the
rest of the JetBrains lineup. Powered by Talyvor Lens — every
AI call gets attributed to the active Track issue, just like the
VS Code extension and the CLI agent.

## Requirements

- JDK 17 (Temurin or any OpenJDK 17 build)

That is the whole list. The Gradle wrapper **is** committed —
`gradlew`, `gradlew.bat` and `gradle/wrapper/gradle-wrapper.jar` are
all tracked — so `./gradlew` downloads and runs the pinned Gradle 8.7
on its own. Do not run `gradle wrapper`: without a system Gradle it
fails outright, and with one it rewrites `gradle-wrapper.properties`
to whatever version you happen to have installed, moving your build
off the version CI provisions.

## Build

```bash
cd jetbrains
./gradlew buildPlugin    # → build/distributions/talyvor-code-0.1.0.zip
```

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
./gradlew buildPlugin verifyPlugin
```

CI runs `./gradlew test buildPlugin --no-daemon` on every push and PR
— see `.github/workflows/ci.yaml`, `jetbrains` job. `verifyPlugin` is
**not** among them: it is a local check, so a plugin-descriptor
problem it would catch does not turn CI red.
