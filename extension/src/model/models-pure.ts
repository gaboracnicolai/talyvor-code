// TS port of agent/internal/model/selector.go. Pure helpers so
// the QuickPick + status bar can pick the right label/icon
// without a vscode runtime dependency. Tests live in
// extension/test/model.test.ts.

export interface ModelProfile {
  id: string;
  displayName: string;
  provider: string;
  speedTier: "fast" | "balanced" | "powerful";
  costTier: "cheap" | "medium" | "expensive";
  bestFor: string[];
  // icon is the codicon name the QuickPick renders. We pick by
  // speed/cost: fast→sparkle, balanced→zap, powerful→rocket,
  // anything else→symbol-misc (used by the non-Claude entries).
  icon: string;
}

export const KNOWN_MODELS: ModelProfile[] = [
  {
    id: "claude-haiku-4-5",
    displayName: "Claude Haiku",
    provider: "Anthropic",
    speedTier: "fast",
    costTier: "cheap",
    bestFor: ["completions", "shell", "commit", "ask"],
    icon: "sparkle",
  },
  {
    id: "claude-sonnet-4-6",
    displayName: "Claude Sonnet",
    provider: "Anthropic",
    speedTier: "balanced",
    costTier: "medium",
    bestFor: ["chat", "tests", "agent", "review"],
    icon: "zap",
  },
  {
    id: "claude-opus-4-6",
    displayName: "Claude Opus",
    provider: "Anthropic",
    speedTier: "powerful",
    costTier: "expensive",
    bestFor: ["complex-agent", "architecture"],
    icon: "rocket",
  },
  {
    id: "gpt-4o",
    displayName: "GPT-4o",
    provider: "OpenAI",
    speedTier: "balanced",
    costTier: "medium",
    bestFor: ["chat", "tests"],
    icon: "symbol-misc",
  },
  {
    id: "gpt-4o-mini",
    displayName: "GPT-4o Mini",
    provider: "OpenAI",
    speedTier: "fast",
    costTier: "cheap",
    bestFor: ["completions", "shell"],
    icon: "symbol-misc",
  },
  {
    id: "mistral-large",
    displayName: "Mistral Large",
    provider: "Mistral",
    speedTier: "balanced",
    costTier: "medium",
    bestFor: ["chat", "agent"],
    icon: "symbol-misc",
  },
];

export function getModel(id: string): ModelProfile | undefined {
  const want = (id ?? "").trim();
  return KNOWN_MODELS.find((m) => m.id === want);
}

export function listModels(): ModelProfile[] {
  return KNOWN_MODELS;
}

// defaultForCommand mirrors the Go DefaultForCommand exactly so
// both surfaces agree on the per-command defaults. Unknown
// commands fall back to Haiku (cheap default).
export function defaultForCommand(command: string): string {
  switch ((command ?? "").trim().toLowerCase()) {
    case "completion":
    case "completions":
    case "shell":
    case "shell-explain":
    case "shell-fix":
    case "commit":
    case "ask":
      return "claude-haiku-4-5";
    case "chat":
    case "test":
    case "tests":
    case "test-gen":
    case "test-generation":
    case "review":
    case "code-review":
    case "run":
    case "agent":
    case "agent-plan":
    case "agent-execute":
      return "claude-sonnet-4-6";
  }
  return "claude-haiku-4-5";
}

// resolveModel applies the documented priority — settings ≈ env
// in the IDE world — and trims whitespace.
export function resolveModel(
  settingValue: string | undefined,
  command: string,
): string {
  const v = (settingValue ?? "").trim();
  if (v) return v;
  return defaultForCommand(command);
}
