"use strict";
// Smoke tests for the Lens + Track clients. We deliberately avoid
// `@vscode/test-electron` here so the test suite compiles cleanly
// in CI without bundling the entire VS Code runtime. Real VS Code
// activation tests land alongside Phase 2's completion provider.
Object.defineProperty(exports, "__esModule", { value: true });
const client_1 = require("../src/lens/client");
const client_2 = require("../src/track/client");
function assert(cond, msg) {
    if (!cond)
        throw new Error("ASSERT: " + msg);
}
async function testIsConfigured() {
    const empty = new client_1.LensClient("", "");
    assert(!empty.isConfigured(), "empty client must report not configured");
    const ok = new client_1.LensClient("http://localhost:8080", "tlv_test");
    assert(ok.isConfigured(), "client with url + key is configured");
}
async function testTrackGetIssueReturnsNullOnMissingConfig() {
    const t = new client_2.TrackClient("", "");
    const res = await t.getIssue("ws-1", "ENG-42");
    assert(res === null, "Track without config must return null");
}
async function main() {
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
//# sourceMappingURL=extension.test.js.map