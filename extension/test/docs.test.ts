// Smoke tests for the docs integration. Exercises:
//   - DocsClient.isConfigured gating
//   - search/ask/getPage URL building + payload shaping via a
//     fetch stub
//   - pickRelevantHit threshold logic
//   - buildHoverQuery + buildHoverMarkdown layout
//   - absolutiseDocsURL + freshnessIcon + renderMarkdown shape

import {
  absolutiseDocsURL,
  buildHoverMarkdown,
  buildHoverQuery,
  freshnessIcon,
  HOVER_RANK_THRESHOLD,
  pickRelevantHit,
  renderMarkdown,
} from "../src/docs/docs-pure";
import { DocsClient } from "../src/docs/docs-client";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── pure helpers ──────────────────────────────────

function testPickRelevantHit(): void {
  const hits = [
    { pageId: "p1", pageTitle: "Low", spaceName: "x", headline: "h", rank: 0.4, source: "fulltext" as const, url: "/p1" },
    { pageId: "p2", pageTitle: "Mid", spaceName: "x", headline: "h", rank: 0.6, source: "both" as const, url: "/p2" },
    { pageId: "p3", pageTitle: "High", spaceName: "x", headline: "h", rank: 0.9, source: "semantic" as const, url: "/p3" },
  ];
  const got = pickRelevantHit(hits);
  assert(got?.pageId === "p2", "should pick first above threshold, got " + got?.pageId);
}

function testPickRelevantHitNoneAboveThreshold(): void {
  const hits = [
    { pageId: "p1", pageTitle: "Low", spaceName: "x", headline: "h", rank: 0.1, source: "fulltext" as const, url: "/p1" },
  ];
  assert(pickRelevantHit(hits) === undefined, "should return undefined");
  assert(HOVER_RANK_THRESHOLD === 0.5, "threshold constant");
}

function testBuildHoverQuery(): void {
  const q = buildHoverQuery("authenticate", ["AuthService", "verifyToken"]);
  assert(q.includes("authenticate"), "word present");
  assert(q.includes("AuthService"), "context present");
  assert(q.split(" ").length <= 3, "capped at 3 tokens");
}

function testBuildHoverQuerySkipsBlanks(): void {
  const q = buildHoverQuery("foo", ["", "Bar", ""]);
  assert(q === "foo Bar", "got: " + q);
}

function testFreshnessIcon(): void {
  assert(freshnessIcon("fresh").emoji === "🟢", "fresh");
  assert(freshnessIcon("warning").emoji === "🟡", "warning");
  assert(freshnessIcon("stale").emoji === "🔴", "stale");
  assert(freshnessIcon("").emoji === "⚪", "unknown empty");
  assert(freshnessIcon("garbage").label === "Unknown", "unknown fallback");
}

function testAbsolutiseDocsURL(): void {
  assert(
    absolutiseDocsURL("/spaces/s/pages/p", "http://docs:8080") ===
      "http://docs:8080/spaces/s/pages/p",
    "leading slash join",
  );
  assert(
    absolutiseDocsURL("https://docs.example/page", "http://docs:8080") ===
      "https://docs.example/page",
    "absolute passthrough",
  );
  assert(
    absolutiseDocsURL("relative/p", "http://docs:8080/") ===
      "http://docs:8080/relative/p",
    "trailing-slash base",
  );
}

function testBuildHoverMarkdown(): void {
  const md = buildHoverMarkdown(
    {
      pageId: "p1",
      pageTitle: "Authentication",
      spaceName: "Engineering",
      headline: "JWT refresh flow…",
      rank: 0.9,
      source: "both",
      url: "/spaces/s1/pages/p1",
    },
    "http://docs:8080",
  );
  assert(md.includes("📄 **Related spec**: Authentication"), "header line");
  assert(md.includes("_in Engineering_"), "space line");
  assert(md.includes("JWT refresh flow…"), "headline");
  assert(md.includes("(http://docs:8080/spaces/s1/pages/p1)"), "absolute url");
}

function testRenderMarkdownBasics(): void {
  const html = renderMarkdown("# Title\n\nA paragraph with **bold** and `code`.\n\n- one\n- two\n");
  assert(html.includes("<h1"), "h1 emitted");
  assert(html.includes("Title"), "heading text");
  assert(html.includes("<strong>bold</strong>"), "bold");
  assert(html.includes("<code"), "inline code");
  assert(html.includes("<ul"), "ul emitted");
  assert(html.includes("<li>one</li>"), "li one");
}

function testRenderMarkdownCodeFence(): void {
  const html = renderMarkdown("```ts\nconst x = 1;\n```");
  assert(html.includes('<pre class="md-code"'), "pre block");
  assert(html.includes("const x = 1;"), "body preserved");
  assert(html.includes('data-lang="ts"'), "lang attr");
}

// ─── DocsClient ────────────────────────────────────

function testIsConfigured(): void {
  assert(!new DocsClient("", "").isConfigured(), "empty unconfigured");
  assert(!new DocsClient("", "k").isConfigured(), "missing url");
  assert(!new DocsClient("u", "").isConfigured(), "missing key");
  assert(new DocsClient("http://d", "k").isConfigured(), "configured");
}

function testBaseURLStripsTrailingSlash(): void {
  const c = new DocsClient("http://docs:8080/", "k");
  assert(c.baseURL() === "http://docs:8080", "trailing slash stripped");
}

async function testSearchDocsReturnsEmptyWhenUnconfigured(): Promise<void> {
  const c = new DocsClient("", "");
  const out = await c.searchDocs("ws-1", "auth", 5);
  assert(out.length === 0, "expected empty");
}

async function testSearchDocsHitsCorrectURL(): Promise<void> {
  const calls: string[] = [];
  const stub: typeof fetch = async (input) => {
    calls.push(String(input));
    return new Response(
      JSON.stringify({
        results: [
          {
            page_id: "p1",
            page_title: "Auth",
            space_name: "Eng",
            headline: "...",
            rank: 0.9,
            source: "both",
            url: "/spaces/s1/pages/p1",
          },
        ],
        total: 1,
      }),
      { status: 200 },
    );
  };
  const originalFetch = globalThis.fetch;
  globalThis.fetch = stub;
  try {
    const c = new DocsClient("http://docs:8080", "k");
    const out = await c.searchDocs("ws-1", "auth flow", 7);
    assert(out.length === 1, "result length");
    assert(out[0].pageTitle === "Auth", "title");
    assert(out[0].rank === 0.9, "rank");
    assert(out[0].source === "both", "source");
    assert(
      calls[0].includes("/v1/workspaces/ws-1/search?q=auth%20flow&limit=7"),
      "url: " + calls[0],
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
}

async function testAskDocsPostsCorrectBody(): Promise<void> {
  let gotBody = "";
  let gotMethod = "";
  const stub: typeof fetch = async (_input, init) => {
    gotMethod = init?.method ?? "GET";
    gotBody = String(init?.body ?? "");
    return new Response(
      JSON.stringify({
        answer: "Use refresh tokens.",
        sources: [{ title: "Auth", url: "/spaces/s1/pages/p1" }],
      }),
      { status: 200 },
    );
  };
  const originalFetch = globalThis.fetch;
  globalThis.fetch = stub;
  try {
    const c = new DocsClient("http://docs:8080", "k");
    const out = await c.askDocs("ws-1", "How does JWT refresh work?");
    assert(out !== null, "answer not null");
    assert(out!.answer.startsWith("Use refresh"), "answer body");
    assert(out!.sources.length === 1, "sources length");
    assert(gotMethod === "POST", "method POST");
    assert(gotBody.includes("How does JWT refresh"), "body has question");
  } finally {
    globalThis.fetch = originalFetch;
  }
}

async function main(): Promise<void> {
  testPickRelevantHit();
  testPickRelevantHitNoneAboveThreshold();
  testBuildHoverQuery();
  testBuildHoverQuerySkipsBlanks();
  testFreshnessIcon();
  testAbsolutiseDocsURL();
  testBuildHoverMarkdown();
  testRenderMarkdownBasics();
  testRenderMarkdownCodeFence();
  testIsConfigured();
  testBaseURLStripsTrailingSlash();
  await testSearchDocsReturnsEmptyWhenUnconfigured();
  await testSearchDocsHitsCorrectURL();
  await testAskDocsPostsCorrectBody();
  // eslint-disable-next-line no-console
  console.log("ok (14 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
