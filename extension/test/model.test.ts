// Smoke tests for the model pure helpers. Validates the catalogue,
// per-command defaults, and the priority resolver. Mirrors the
// Go-side tests in agent/internal/model/selector_test.go so the
// two surfaces stay aligned.

import {
  KNOWN_MODELS,
  defaultForCommand,
  getModel,
  listModels,
  resolveModel,
} from "../src/model/models-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function testListContainsExpectedModels(): void {
  const ids = listModels().map((m) => m.id);
  for (const want of [
    "claude-haiku-4-6",
    "claude-sonnet-4-6",
    "claude-opus-4-6",
    "gpt-4o",
    "gpt-4o-mini",
    "mistral-large",
  ]) {
    assert(ids.includes(want), `missing model ${want}`);
  }
}

function testGetModelReturnsProfile(): void {
  const m = getModel("claude-sonnet-4-6");
  assert(m !== undefined, "expected profile");
  assert(m!.provider === "Anthropic", "provider");
  assert(m!.speedTier === "balanced", "speedTier");
}

function testGetModelUnknownReturnsUndefined(): void {
  assert(getModel("nope") === undefined, "undefined for unknown");
  assert(getModel("") === undefined, "undefined for empty");
}

function testDefaultsMatchAgentBehavior(): void {
  // These pairings must match agent/internal/model/selector.go
  // exactly so the CLI and extension don't drift.
  const cases: Record<string, string> = {
    completion: "claude-haiku-4-6",
    completions: "claude-haiku-4-6",
    shell: "claude-haiku-4-6",
    commit: "claude-haiku-4-6",
    ask: "claude-haiku-4-6",
    chat: "claude-sonnet-4-6",
    test: "claude-sonnet-4-6",
    tests: "claude-sonnet-4-6",
    review: "claude-sonnet-4-6",
    run: "claude-sonnet-4-6",
    agent: "claude-sonnet-4-6",
    unknown: "claude-haiku-4-6",
  };
  for (const [cmd, want] of Object.entries(cases)) {
    const got = defaultForCommand(cmd);
    assert(got === want, `defaultForCommand(${cmd}) = ${got}, want ${want}`);
  }
}

function testResolveModelPriority(): void {
  // 1. Setting > default
  assert(resolveModel("gpt-4o", "chat") === "gpt-4o", "setting wins");
  // 2. Default applied when setting is empty
  assert(resolveModel("", "tests") === "claude-sonnet-4-6", "default for tests");
  // 3. Whitespace-only setting treated as empty
  assert(resolveModel("   ", "completions") === "claude-haiku-4-6", "whitespace → default");
  // 4. Trimmed setting wins
  assert(resolveModel("  gpt-4o  ", "chat") === "gpt-4o", "trim setting");
}

function testCatalogueShape(): void {
  // Each profile must have all required fields populated.
  for (const m of KNOWN_MODELS) {
    assert(m.id.length > 0, "id empty");
    assert(m.displayName.length > 0, "displayName empty: " + m.id);
    assert(m.provider.length > 0, "provider empty: " + m.id);
    assert(
      ["fast", "balanced", "powerful"].includes(m.speedTier),
      "bad speedTier: " + m.id,
    );
    assert(
      ["cheap", "medium", "expensive"].includes(m.costTier),
      "bad costTier: " + m.id,
    );
    assert(m.bestFor.length > 0, "bestFor empty: " + m.id);
    assert(m.icon.length > 0, "icon empty: " + m.id);
  }
}

async function main(): Promise<void> {
  testListContainsExpectedModels();
  testGetModelReturnsProfile();
  testGetModelUnknownReturnsUndefined();
  testDefaultsMatchAgentBehavior();
  testResolveModelPriority();
  testCatalogueShape();
  // eslint-disable-next-line no-console
  console.log("ok (6 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
