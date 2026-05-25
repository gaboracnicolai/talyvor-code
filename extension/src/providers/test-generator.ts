// TestGenerator — orchestrates Lens calls for test generation.
// Wraps the pure prompt + filename helpers in test-generator-pure
// with the LensClient + CostTracker plumbing.

import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { CostTracker, estimateCostUSD } from "./cost-tracker";
import {
  buildTestPrompt,
  frameworkFor,
  sanitiseGenerated,
  suggestTestFileName,
  systemPromptFor,
} from "./test-generator-pure";

export interface GeneratedTests {
  code: string;
  fileName: string;
  framework: string;
  language: string;
  costUSD: number;
}

// MODEL_FOR_TESTS is intentionally sonnet rather than haiku. Test
// quality matters more than latency — a wrong test is worse than
// no test, and Sonnet is meaningfully better at producing
// runnable code than Haiku.
const MODEL_FOR_TESTS = "claude-sonnet-4-6";

// MAX_TOKENS=2000 covers most realistic test suites without
// truncation. Past 2000 tokens we're usually generating a second
// test file's worth of code — out of scope for one round-trip.
const MAX_TOKENS = 2000;

export class TestGenerator {
  constructor(
    private lens: LensClient,
    private tracker: CostTracker,
  ) {}

  async generateTests(
    code: string,
    languageId: string,
    sourcePath: string,
    config: LensConfig,
  ): Promise<GeneratedTests> {
    const system = systemPromptFor(languageId);
    const user = buildTestPrompt(code, languageId, fileBaseName(sourcePath));
    const res = await this.lens.completeWithUsage(
      [{ role: "user", content: `${system}\n\n${user}` }],
      MODEL_FOR_TESTS,
      "test-gen",
      config.workspaceId,
      config.activeIssue,
      MAX_TOKENS,
    );
    const cost = estimateCostUSD(res.inputTokens, res.outputTokens);
    this.tracker.recordCompletion(
      res.inputTokens + res.outputTokens,
      cost,
      config.activeIssue,
    );
    return {
      code: sanitiseGenerated(res.text),
      fileName: suggestTestFileName(sourcePath, languageId),
      framework: frameworkFor(languageId),
      language: languageId,
      costUSD: cost,
    };
  }
}

function fileBaseName(p: string): string {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i >= 0 ? p.substring(i + 1) : p;
}

// Re-export the pure helpers for callers that only want the
// utilities (e.g. command handlers building filename previews
// before launching the panel).
export {
  buildTestPrompt,
  frameworkFor,
  sanitiseGenerated,
  suggestTestFileName,
  systemPromptFor,
};
