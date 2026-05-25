// Smoke tests for the Lens + Track clients. We deliberately avoid
// `@vscode/test-electron` here so the test suite compiles cleanly
// in CI without bundling the entire VS Code runtime. Real VS Code
// activation tests land alongside Phase 2's completion provider.

import { LensClient } from "../src/lens/client";
import { TrackClient } from "../src/track/client";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

async function testIsConfigured(): Promise<void> {
  const empty = new LensClient("", "");
  assert(!empty.isConfigured(), "empty client must report not configured");

  const ok = new LensClient("http://localhost:8080", "tlv_test");
  assert(ok.isConfigured(), "client with url + key is configured");
}

async function testTrackGetIssueReturnsNullOnMissingConfig(): Promise<void> {
  const t = new TrackClient("", "");
  const res = await t.getIssue("ws-1", "ENG-42");
  assert(res === null, "Track without config must return null");
}

async function main(): Promise<void> {
  await testIsConfigured();
  await testTrackGetIssueReturnsNullOnMissingConfig();
  // eslint-disable-next-line no-console
  console.log("ok");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
