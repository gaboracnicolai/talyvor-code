// Wire-shape types for Talyvor Lens. We deliberately mirror the
// Anthropic Messages API request/response shape since Lens passes
// it through verbatim — keeping the type names familiar makes the
// proxy boundary read as a thin shim rather than a fresh protocol.

export interface LensConfig {
  url: string;
  apiKey: string;
  workspaceId: string;
  activeIssue: string;
  model: string;
  trackUrl: string;
  trackApiKey: string;
  enableCompletions: boolean;
}

export interface Message {
  role: "user" | "assistant" | "system";
  content: string;
}

export interface CompletionRequest {
  model: string;
  messages: Message[];
  max_tokens: number;
  stream?: boolean;
}

export interface CompletionResponse {
  content: Array<{ type: string; text: string }>;
  usage: {
    input_tokens: number;
    output_tokens: number;
  };
}

export interface CostInfo {
  issueId: string;
  totalCostUSD: number;
  tokens: number;
  lastUpdated: Date;
}

// LensError carries the user-visible message + the original status
// code so the extension can map specific failures (401 → "API key
// is invalid") without re-parsing strings.
export class LensError extends Error {
  constructor(
    message: string,
    public readonly status: number,
  ) {
    super(message);
    this.name = "LensError";
  }
}
