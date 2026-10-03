import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AGENT_CSS, CHAT_CSS, COST_CSS, DOCS_CSS, ISSUE_CSS, REVIEW_CSS, TEST_CSS, estimateHTML,
} from "./theme-pure";
import { freshnessIcon } from "../docs/docs-pure";
import { verdictBadge } from "../commands/pr-review-pure";

// A colour literal is a colour the user's theme cannot change: #1e1e1e is a dark slab on Light+.
const LITERAL = /#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?)\(/;

test("every panel takes every colour from the user's VS Code theme", () => {
  const sheets = {
    chat: CHAT_CSS, agent: AGENT_CSS, docs: DOCS_CSS, tests: TEST_CSS,
    cost: COST_CSS, issue: ISSUE_CSS, review: REVIEW_CSS,
  };
  for (const [name, css] of Object.entries(sheets)) {
    assert.doesNotMatch(css, LITERAL, `${name} panel hardcodes a colour`);
    assert.match(css, /var\(--vscode-/, `${name} panel reads nothing from the theme`);
    assert.match(css, /\.eyebrow\{[^}]*text-transform:uppercase/, `${name} panel lost the eyebrow`);
  }
  for (const status of ["fresh", "warning", "stale", "unknown"]) {
    assert.match(freshnessIcon(status).color, /^var\(--vscode-/, `docs freshness "${status}"`);
  }
  for (const v of ["APPROVE", "REQUEST CHANGES", "NEEDS DISCUSSION"] as const) {
    assert.match(verdictBadge(v).color, /^var\(--vscode-/, `review verdict "${v}"`);
  }
});

test("an estimated cost renders with its tilde and an est. label, never as a bare figure", () => {
  const html = estimateHTML(0.0123);
  assert.ok(html.includes("~$0.0123"), html);
  assert.ok(html.includes(">est.<"), html);
  assert.ok(html.includes("not your bill"), "the disclaimer rides along as the tooltip");
});
