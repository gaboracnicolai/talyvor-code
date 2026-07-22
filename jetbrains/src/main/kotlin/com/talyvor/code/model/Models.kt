// Pure model catalogue — Kotlin port of the VS Code extension's
// model/models-pure.ts and the Go agent's internal/model/selector.go.
//
// Deliberately free of any IntelliJ Platform dependency so it can be
// exercised with plain JUnit (see ModelsTest). Keeping the JetBrains
// per-command model defaults in lock-step with the other two surfaces
// is the whole point — drift here means the same command picks a
// different model depending on which client the developer is using.

package com.talyvor.code.model

enum class SpeedTier { FAST, BALANCED, POWERFUL }

enum class CostTier { CHEAP, MEDIUM, EXPENSIVE }

/**
 * ModelProfile mirrors the ModelProfile interface in models-pure.ts.
 * `bestFor` lists the command tags the model is the best default for;
 * it drives the model-picker's descriptive subtext.
 */
data class ModelProfile(
    val id: String,
    val displayName: String,
    val provider: String,
    val speedTier: SpeedTier,
    val costTier: CostTier,
    val bestFor: List<String>,
)

object Models {
    const val DEFAULT_MODEL: String = "claude-haiku-4-5"

    // KNOWN mirrors KNOWN_MODELS in models-pure.ts exactly (same ids,
    // providers, tiers, order). The VS Code package.json model enum
    // differs slightly (it lists llama-3.1-70b and omits Opus); the
    // richer pure catalogue is treated as authoritative because it is
    // the one the model-picker + tests consume on all surfaces.
    val KNOWN: List<ModelProfile> = listOf(
        ModelProfile(
            id = "claude-haiku-4-5",
            displayName = "Claude Haiku",
            provider = "Anthropic",
            speedTier = SpeedTier.FAST,
            costTier = CostTier.CHEAP,
            bestFor = listOf("completions", "shell", "commit", "ask"),
        ),
        ModelProfile(
            id = "claude-sonnet-4-6",
            displayName = "Claude Sonnet",
            provider = "Anthropic",
            speedTier = SpeedTier.BALANCED,
            costTier = CostTier.MEDIUM,
            bestFor = listOf("chat", "tests", "agent", "review"),
        ),
        ModelProfile(
            id = "claude-opus-4-6",
            displayName = "Claude Opus",
            provider = "Anthropic",
            speedTier = SpeedTier.POWERFUL,
            costTier = CostTier.EXPENSIVE,
            bestFor = listOf("complex-agent", "architecture"),
        ),
        ModelProfile(
            id = "gpt-4o",
            displayName = "GPT-4o",
            provider = "OpenAI",
            speedTier = SpeedTier.BALANCED,
            costTier = CostTier.MEDIUM,
            bestFor = listOf("chat", "tests"),
        ),
        ModelProfile(
            id = "gpt-4o-mini",
            displayName = "GPT-4o Mini",
            provider = "OpenAI",
            speedTier = SpeedTier.FAST,
            costTier = CostTier.CHEAP,
            bestFor = listOf("completions", "shell"),
        ),
        ModelProfile(
            id = "mistral-large",
            displayName = "Mistral Large",
            provider = "Mistral",
            speedTier = SpeedTier.BALANCED,
            costTier = CostTier.MEDIUM,
            bestFor = listOf("chat", "agent"),
        ),
    )

    fun list(): List<ModelProfile> = KNOWN

    /** get returns the profile for [id], or null for an unknown/blank id. */
    fun get(id: String?): ModelProfile? {
        val want = (id ?: "").trim()
        if (want.isEmpty()) return null
        return KNOWN.firstOrNull { it.id == want }
    }

    /**
     * defaultForCommand mirrors the Go DefaultForCommand + the TS
     * defaultForCommand exactly so the CLI, VS Code, and JetBrains
     * surfaces never disagree on a command's default model. Unknown
     * commands fall back to the cheap default (Haiku).
     */
    fun defaultForCommand(command: String?): String =
        when ((command ?: "").trim().lowercase()) {
            "completion", "completions", "shell", "shell-explain",
            "shell-fix", "commit", "ask",
            -> "claude-haiku-4-5"
            "chat", "test", "tests", "test-gen", "test-generation",
            "review", "code-review", "run", "agent", "agent-plan",
            "agent-execute",
            -> "claude-sonnet-4-6"
            else -> DEFAULT_MODEL
        }

    /**
     * resolveModel applies the documented priority: an explicit
     * (non-blank) setting wins; otherwise fall back to the per-command
     * default. Whitespace-only settings are treated as unset.
     */
    fun resolveModel(settingValue: String?, command: String): String {
        val v = (settingValue ?: "").trim()
        return if (v.isNotEmpty()) v else defaultForCommand(command)
    }
}
