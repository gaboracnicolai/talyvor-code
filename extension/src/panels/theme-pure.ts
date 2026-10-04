// theme-pure.ts — the stylesheet every Talyvor webview panel is built from.
//
// ⚠ AN EXTENSION INHERITS THE USER'S THEME, AND THESE PANELS DO NOT FIGHT IT. They used to paint
// #1e1e1e and an amber accent into every editor, so someone on Light+ or Solarized got a dark slab in
// the middle of a light window. Every colour below is a `--vscode-*` variable — VS Code injects the
// active theme's values into each webview and updates them live — so the panels look native in dark,
// light and high-contrast themes, and follow a theme switch without being reopened.
//
// WHAT PORTS FROM THE SITE IS THE GRAMMAR, NOT THE HEX (the CLI's internal/ui says the same):
//   · .eyebrow — the small uppercase label that names a block before it says anything: 11px, 0.24em
//     tracking, the editor's monospace, muted. The site's token, minus the site's colour.
//   · .figure — numbers set in tabular figures, so they read as measured and line up.
//   · ONE accent — the theme's link colour — spent on the attributed issue (.chip) and nothing else
//     decorative. Buttons are the theme's buttons; focus is the theme's focus ring; other labels are
//     a muted .tag.
//   · an estimate is labelled as one (estimateHTML), never styled to pass for a measurement.

import type { DiffLine } from "../agent/agent-pure";
import { costDisclaimerLines, formatCostEstimate } from "../track/cost-label-pure";
import { escapeHTML } from "./chat-pure";

const BASE_CSS = `:root{
--t-fg:var(--vscode-foreground);
--t-muted:var(--vscode-descriptionForeground);
--t-bg:var(--vscode-editor-background);
--t-chrome:var(--vscode-sideBar-background,var(--vscode-editor-background));
--t-surface:var(--vscode-editorWidget-background);
--t-rule:var(--vscode-panel-border);
--t-code:var(--vscode-textCodeBlock-background);
--t-accent:var(--vscode-textLink-foreground);
--t-focus:var(--vscode-focusBorder);
--t-ok:var(--vscode-testing-iconPassed);
--t-warn:var(--vscode-editorWarning-foreground);
--t-err:var(--vscode-errorForeground);
--t-add:var(--vscode-gitDecoration-addedResourceForeground);
--t-del:var(--vscode-gitDecoration-deletedResourceForeground);
--t-mod:var(--vscode-gitDecoration-modifiedResourceForeground);
--t-mono:var(--vscode-editor-font-family);
}
body{font-family:var(--vscode-font-family);font-size:var(--vscode-font-size);color:var(--t-fg);background:var(--t-bg);margin:0;line-height:1.45}
body>header{display:flex;align-items:center;gap:10px;padding:8px 12px;border-bottom:1px solid var(--t-rule);background:var(--t-chrome)}
body>footer{padding:6px 12px;border-top:1px solid var(--t-rule);background:var(--t-chrome);font-size:11px;color:var(--t-muted)}
.eyebrow{font-family:var(--t-mono);font-size:11px;line-height:1.2;letter-spacing:.24em;text-transform:uppercase;font-weight:400;color:var(--t-muted)}
h2.eyebrow,h3.eyebrow{margin:16px 0 6px}
.figure{font-variant-numeric:tabular-nums;color:var(--t-fg)}
[title]{cursor:help}
button[title]{cursor:pointer}
.chip{font-family:var(--t-mono);font-size:11px;color:var(--t-accent);border:1px solid var(--t-rule);border-radius:3px;padding:1px 6px}
.chip:empty{display:none}
.tag{font-family:var(--t-mono);font-size:10px;letter-spacing:.12em;text-transform:uppercase;color:var(--t-muted);border:1px solid var(--t-rule);border-radius:3px;padding:1px 6px}
.muted{color:var(--t-muted)}
a{color:var(--vscode-textLink-foreground)}
a:hover{color:var(--vscode-textLink-activeForeground)}
code,pre{font-family:var(--t-mono)}
button{font-family:inherit;font-size:12px;background:var(--vscode-button-background);color:var(--vscode-button-foreground);border:1px solid var(--vscode-button-border,transparent);border-radius:2px;padding:4px 12px;cursor:pointer}
button:hover{background:var(--vscode-button-hoverBackground)}
button.ghost{background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}
button.ghost:hover{background:var(--vscode-button-secondaryHoverBackground)}
button:disabled{opacity:.5;cursor:not-allowed}
textarea,input[type=text],input[type=search]{background:var(--vscode-input-background);color:var(--vscode-input-foreground);border:1px solid var(--vscode-input-border,var(--t-rule));border-radius:2px;padding:6px 8px;font-family:inherit;font-size:inherit;box-sizing:border-box}
textarea::placeholder,input::placeholder{color:var(--vscode-input-placeholderForeground)}
button:focus-visible,textarea:focus,input:focus{outline:1px solid var(--t-focus);outline-offset:-1px}
.error{color:var(--t-err);background:var(--vscode-inputValidation-errorBackground);border:1px solid var(--vscode-inputValidation-errorBorder);border-radius:3px;padding:8px 12px}
.dots{display:flex;gap:6px}
.dots i{width:6px;height:6px;border-radius:50%;background:var(--t-muted);animation:bounce 1.2s infinite}
.dots i:nth-child(2){animation-delay:.15s}
.dots i:nth-child(3){animation-delay:.3s}
@keyframes bounce{0%,80%,100%{transform:scale(.6);opacity:.4}40%{transform:scale(1);opacity:1}}
`;

export const CHAT_CSS = `${BASE_CSS}
body{display:flex;flex-direction:column;height:100vh}
body>header button{margin-left:auto}
main{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:10px}
.msg{padding:8px 12px;border-radius:4px;max-width:90%}
.msg.user{align-self:flex-end;background:var(--vscode-editor-inactiveSelectionBackground)}
.msg.assistant{align-self:flex-start;background:var(--t-surface);border:1px solid var(--t-rule)}
.msg.assistant p{margin:0 0 8px}
.msg.assistant p:last-child{margin-bottom:0}
.code{background:var(--t-code);border:1px solid var(--t-rule);border-radius:4px;margin:6px 0;overflow:hidden}
.code-head{display:flex;align-items:center;gap:6px;padding:4px 8px;border-bottom:1px solid var(--t-rule)}
.code-head span{flex:1}
.code-head button{padding:1px 6px;font-size:10px}
.code pre{margin:0;padding:8px;font-size:12px;white-space:pre-wrap;overflow-x:auto}
.thinking{display:flex;gap:4px;align-self:flex-start;padding:8px 12px}
.thinking i{width:6px;height:6px;border-radius:50%;background:var(--t-muted);animation:bounce 1.2s infinite}
.thinking i:nth-child(2){animation-delay:.15s}
.thinking i:nth-child(3){animation-delay:.3s}
.msg.assistant.streaming .stream-body{white-space:pre-wrap;font-family:inherit}
.msg.assistant.streaming .caret{display:inline-block;color:var(--vscode-editorCursor-foreground);animation:blink 1s steps(2,start) infinite;margin-left:1px;font-weight:bold}
@keyframes blink{to{visibility:hidden}}
.error{font-size:12px;align-self:stretch}
form{padding:8px 12px;border-top:1px solid var(--t-rule);background:var(--t-chrome)}
textarea{width:100%;resize:none}
.composer-row{display:flex;align-items:center;gap:10px;margin-top:6px;font-size:11px;color:var(--t-muted)}
.composer-row button{margin-left:auto;padding:4px 16px}
.shake{animation:shake .3s}
@keyframes shake{0%,100%{transform:translateX(0)}25%{transform:translateX(-3px)}75%{transform:translateX(3px)}}
`;

export const AGENT_CSS = `${BASE_CSS}
body{display:flex;flex-direction:column;height:100vh}
body>header .status{margin-left:auto}
main{flex:1;overflow-y:auto;padding:12px}
.idle{display:flex;flex-direction:column;gap:8px;max-width:680px}
.hint{color:var(--t-muted);margin:0}
textarea{resize:vertical}
.examples{color:var(--t-muted);font-size:11px}
.examples ul{margin:4px 0 0 16px;padding:0}
.idle button{align-self:flex-start}
.progress{display:flex;flex-direction:column;align-items:center;padding:32px;color:var(--t-muted)}
.progress .dots{margin-bottom:12px}
.completed{padding:12px}
.completed h2{color:var(--t-ok);margin:0 0 8px;font-size:15px}
.review .plan ul{margin:0;padding-left:18px}
.change{border:1px solid var(--t-rule);border-radius:4px;margin:8px 0;overflow:hidden}
.change.approved{border-color:var(--t-ok)}
.change.rejected{border-color:var(--t-err);opacity:.6}
.change header,.heal-attempt header{background:var(--t-surface);padding:6px 8px;display:flex;align-items:center;gap:8px;border-bottom:1px solid var(--t-rule)}
.op{font-family:var(--t-mono);font-size:10px;letter-spacing:.12em;padding:1px 6px;border-radius:3px;border:1px solid currentColor}
.op-create{color:var(--t-add)}
.op-modify{color:var(--t-mod)}
.op-delete{color:var(--t-del)}
.change code{font-size:12px;flex:1}
.change header button{padding:2px 10px;font-size:11px}
pre.diff{margin:0;padding:8px;background:var(--t-code);overflow:auto;font-size:12px;line-height:1.5;white-space:pre}
.dh,.dc{color:var(--t-muted);display:block}
.da{color:var(--t-add);display:block}
.dr{color:var(--t-del);display:block}
.feedback{padding:8px;background:var(--t-surface);font-size:11px;color:var(--t-muted);border-top:1px solid var(--t-rule)}
.feedback button{margin-left:8px;padding:1px 8px;font-size:11px}
.actions{padding:12px 0;display:flex;gap:8px}
.healing .attempts{display:flex;flex-direction:column;gap:8px;margin-top:12px}
.heal-attempt{border:1px solid var(--t-rule);border-radius:4px;overflow:hidden}
.heal-attempt.ok{border-color:var(--t-ok)}
.heal-attempt.fail{border-color:var(--t-warn)}
.heal-attempt .op{color:var(--t-muted)}
.heal-attempt .status-pill{margin-left:auto}
.heal-attempt.ok .status-pill{color:var(--t-ok)}
.heal-attempt.fail .status-pill{color:var(--t-err)}
pre.heal-err{margin:0;padding:8px;background:var(--t-code);color:var(--t-err);font-size:11px;line-height:1.5;white-space:pre-wrap;max-height:200px;overflow:auto}
.heal-fixes{list-style:none;padding:6px 10px;margin:0;font-size:11px;color:var(--t-muted)}
.heal-fixes li{padding:2px 0}
`;

export const DOCS_CSS = `${BASE_CSS}
body{display:flex;flex-direction:column;height:100vh;line-height:1.5}
body>header button{margin-left:auto}
main{flex:1;overflow-y:auto;padding:12px}
form{display:flex;gap:6px;margin-bottom:12px}
input[type=search],input[type=text]{flex:1}
.results{list-style:none;padding:0;margin:0;display:flex;flex-direction:column;gap:8px}
.result{padding:8px 10px;border:1px solid var(--t-rule);border-radius:4px;cursor:pointer}
.result:hover{border-color:var(--t-focus)}
.r-title{font-weight:600}
.r-title .space{font-size:11px;color:var(--t-muted);font-weight:400;margin-left:6px}
.r-headline{font-size:12px;color:var(--t-muted);margin-top:4px}
.r-meta{margin-top:6px;display:flex;gap:8px;align-items:center;font-size:11px}
.page-header h1{margin:0 0 6px;font-size:20px;font-weight:500}
.page-meta{display:flex;gap:12px;font-size:11px;color:var(--t-muted);margin-bottom:6px;flex-wrap:wrap;align-items:center}
.freshness{font-weight:500}
.verified{color:var(--t-ok)}
.needs{color:var(--t-warn)}
.page-actions{display:flex;gap:6px;margin:6px 0 12px}
.md-body h1,.md-body h2,.md-body h3{margin-top:16px;font-weight:500}
.md-h1{font-size:18px}
.md-h2{font-size:16px}
.md-h3{font-size:14px}
.md-p{margin:6px 0}
.md-code{background:var(--t-code);border:1px solid var(--t-rule);border-radius:4px;padding:8px;font-family:var(--t-mono);font-size:12px;overflow-x:auto}
.md-ic{background:var(--t-code);padding:1px 4px;border-radius:3px;font-family:var(--t-mono);font-size:11px}
.md-ul,.md-ol{margin:6px 0;padding-left:20px}
.ask h2.eyebrow{margin:0 0 8px}
.ask .q{padding:6px 8px;background:var(--t-surface);border-radius:3px}
.ask .answer{margin-top:8px}
.sources{margin:6px 0 12px;padding-left:18px;font-size:12px}
.ask .dots{padding:12px 0}
`;

export const TEST_CSS = `${BASE_CSS}
.meta{display:flex;gap:18px;padding:8px 12px;border-bottom:1px solid var(--t-rule);font-size:11px}
.meta>div{display:flex;align-items:center;gap:8px}
.meta code{background:var(--t-code);padding:1px 6px;border-radius:3px}
.actions{display:flex;gap:6px;padding:8px 12px;border-bottom:1px solid var(--t-rule)}
pre{margin:0;padding:12px;background:var(--t-code);overflow:auto;font-size:12px;white-space:pre;line-height:1.5}
`;

// The three single-view panels — cost, issue, PR review — share a document layout.
const DOC_CSS = `${BASE_CSS}
main{padding:16px 18px;max-width:920px}
.big{font-size:24px;font-weight:500}
.kv{display:grid;grid-template-columns:160px 1fr;gap:8px 12px;align-items:baseline;margin:0}
.kv dt{margin:0}
.kv dd{margin:0}
table{width:100%;border-collapse:collapse;margin-top:8px;font-size:12px}
th{text-align:left;padding:4px 8px;border-bottom:1px solid var(--t-rule)}
td{padding:4px 8px;border-bottom:1px solid var(--t-rule)}
.md-body h1,.md-body h2,.md-body h3{margin-top:18px;font-weight:500;border-bottom:1px solid var(--t-rule);padding-bottom:4px}
.md-body h2:first-child{margin-top:0}
.md-h1{font-size:18px}
.md-h2{font-size:15px}
.md-h3{font-size:13px;border-bottom:0;padding-bottom:0}
.md-p{margin:6px 0}
.md-code{background:var(--t-code);border:1px solid var(--t-rule);border-radius:4px;padding:8px;font-family:var(--t-mono);font-size:12px;overflow-x:auto}
.md-ic{background:var(--t-code);padding:1px 4px;border-radius:3px;font-family:var(--t-mono);font-size:11px}
.md-ul,.md-ol{margin:6px 0;padding-left:20px}
`;

export const COST_CSS = DOC_CSS;

export const ISSUE_CSS = `${DOC_CSS}
h1{font-size:18px;font-weight:500;margin:0 0 8px}
.desc{margin-top:16px;white-space:pre-wrap}
`;

export const REVIEW_CSS = `${DOC_CSS}
body{display:flex;flex-direction:column;height:100vh;line-height:1.55}
body>header{flex-wrap:wrap}
main{flex:1;overflow-y:auto}
.verdict{font-family:var(--t-mono);font-size:11px;letter-spacing:.12em;padding:1px 8px;border-radius:3px;border:1px solid currentColor}
.meta{font-family:var(--t-mono);font-size:11px;color:var(--t-muted)}
body>header button{margin-left:auto}
body>header button + button{margin-left:0}
`;

/**
 * A locally estimated cost as a panel shows it: the figure keeps its "~", an "est." eyebrow sits
 * beside it, and both carry the disclaimer as a tooltip. Every panel figure from cost-tracker's flat
 * rate goes through here, so none of them can render as a bare "$0.0123" that reads like the bill.
 */
export function estimateHTML(usd: number): string {
  const note = costDisclaimerLines()[0];
  return `<span class="figure" title="${note}">${formatCostEstimate(usd)}</span> <span class="eyebrow" title="${note}">est.</span>`;
}

// diffHTML renders a diff as the rows of pre.diff: one display:block span per line.
//
// ⚠ NO NEWLINE BETWEEN THE ROWS. pre.diff is white-space:pre, so a "\n" after a block span is a
// second, empty row — every line of the Agent panel's diff used to have a blank line under it.
export function diffHTML(lines: DiffLine[]): string {
  return lines.map((line) => {
    switch (line.kind) {
      case "header":
        return `<span class="dh">${escapeHTML(line.text)}</span>`;
      case "context":
        return `<span class="dc"> ${escapeHTML(line.text)}</span>`;
      case "add":
        return `<span class="da">+${escapeHTML(line.text)}</span>`;
      case "remove":
        return `<span class="dr">-${escapeHTML(line.text)}</span>`;
    }
  }).join("");
}
