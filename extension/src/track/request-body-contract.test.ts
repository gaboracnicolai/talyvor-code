import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";

// EVERY FIELD THIS EXTENSION PUTS IN A REQUEST BODY TO TALYVOR-TRACK IS ONE THAT SERVER ACCEPTS.
//
// ⚠ THE DEFECT THIS EXISTS FOR SHIPPED, AND NEITHER REPOSITORY'S CI COULD SEE IT.
// `addComment` sent `{ content, author_id }`. Track decodes that route into `model.Comment`, whose
// json tags are id / issue_id / author_id / body / edited_at / created_at / updated_at — no
// `content` — and Track's `internal/httpx.DecodeJSON` calls `dec.DisallowUnknownFields()`, so the
// request was a hard 400 BAD_JSON before any handler logic ran. Every "agent task completed" note
// this extension ever tried to leave was refused. Measured by executing the stdlib decoder against
// a verbatim transcription of that struct at talyvor-track 7ad05ce3:
//
//     {"content":"…","author_id":"talyvor-code"}  ->  json: unknown field "content"
//     {"body":"…","author_id":"talyvor-code"}     ->  <nil>
//
// ⚠⚠ WHY NOTHING CAUGHT IT. Talyvor-track ships `cmd/track/spa_request_body_contract_test.go`,
// which joins its OWN SPA's request bodies to those handlers — it knows nothing about this
// extension. This repository's CI never sees Track's source. **The extension is a second client of
// that API and it lived in the gap between two censuses that each cover one side.** The `catch {}`
// with no `res.ok` check finished the job: a request that failed on every call produced the same
// trace as one that worked.
//
// ⚠⚠⚠ WHAT THIS GUARD CHECKS ON EVERY RUN AND WHAT IT DOES NOT — SAID PLAINLY, BECAUSE HALF OF IT
// IS NOT CHECKABLE HERE AND A GUARD THAT IMPLIES OTHERWISE IS WORSE THAN NONE.
//   CHECKED IN CI: the key set this extension puts on the wire, extracted from the source that
//     sends it, against the table below.
//   NOT CHECKED IN CI: that the table still matches talyvor-track. That half is a RECORDED
//     MEASUREMENT at the SHA named above — this repository's CI checks out talyvor-code alone.
//     If Track renames a json tag, this guard stays green and the request starts failing. The
//     table therefore carries the SHA it was read at, so a reader can tell how old the claim is.
//     The same limit is stated in talyvor-track's own census for its estate tags.

function extensionRoot(): string {
  let dir = __dirname;
  for (let i = 0; i < 8; i++) {
    if (fs.existsSync(path.join(dir, "package.json"))) return dir;
    dir = path.dirname(dir);
  }
  throw new Error("cannot locate the extension root from " + __dirname);
}

// ⚠ THE TESTS RUN FROM out/, SO __dirname IS THE COMPILED TREE — the sibling guard in this
// directory records scanning an EMPTY SET and passing unconditionally for exactly that reason.
const clientPath = path.join(extensionRoot(), "src", "track", "client.ts");
if (!fs.existsSync(clientPath)) {
  throw new Error("src/track/client.ts not found at " + clientPath + " — this guard would scan nothing");
}

/**
 * The json keys each Track write route accepts, transcribed from talyvor-track at 7ad05ce3.
 *
 * POST /v1/workspaces/{wsID}/issues/{id}/comments decodes `model.Comment` (internal/model/model.go)
 * and then OVERWRITES author_id with the verified session member (SEC-5) — so author_id is
 * accepted and ignored, which is why sending it is harmless and sending `content` is not.
 */
const accepted: Record<string, string[]> = {
  "issues/{}/comments": [
    "id",
    "issue_id",
    "author_id",
    "body",
    "edited_at",
    "created_at",
    "updated_at",
  ],
};

/** Every `body: JSON.stringify({ … })` literal in the Track client, with its key set. */
function bodySites(src: string): { keys: string[]; raw: string }[] {
  const out: { keys: string[]; raw: string }[] = [];
  const marker = "body: JSON.stringify({";
  let i = src.indexOf(marker);
  while (i !== -1) {
    const open = src.indexOf("{", i + "body: JSON.stringify(".length);
    let depth = 0;
    let end = -1;
    for (let j = open; j < src.length; j++) {
      if (src[j] === "{") depth++;
      else if (src[j] === "}") {
        depth--;
        if (depth === 0) {
          end = j;
          break;
        }
      }
    }
    if (end === -1) throw new Error("unterminated body literal at offset " + i);
    const raw = src.slice(open, end + 1);
    // `{ body: content, author_id: "x" }` and the shorthand `{ content }` both yield their keys
    const keys = new Set<string>();
    for (const m of raw.matchAll(/(?:^|[{,])\s*([A-Za-z_][A-Za-z0-9_]*)\s*[:,}]/g)) keys.add(m[1]);
    out.push({ keys: [...keys].sort(), raw: raw.replace(/\s+/g, " ") });
    i = src.indexOf(marker, end);
  }
  return out;
}

test("the Track client's request bodies are found at all", () => {
  // ⚠ A FLOOR, BECAUSE THIS GUARD REPORTS AN ABSENCE AND A SCANNER THAT HAS GONE BLIND REPORTS THE
  // EMPTY SET — which agrees with everything. One write site at the time of writing; floored at 1.
  const sites = bodySites(fs.readFileSync(clientPath, "utf8"));
  assert.ok(
    sites.length >= 1,
    `found ${sites.length} request bodies in src/track/client.ts; expected at least 1. ` +
      "Do not lower this to make a red go green — find out why the scan stopped seeing them.",
  );
});

test("every field the Track client sends is one Track's handler accepts", () => {
  const src = fs.readFileSync(clientPath, "utf8");
  const allowed = new Set(accepted["issues/{}/comments"]);
  const offenders: string[] = [];
  for (const site of bodySites(src)) {
    for (const key of site.keys) {
      if (!allowed.has(key)) offenders.push(`${key}  (in ${site.raw})`);
    }
  }
  assert.deepEqual(
    offenders,
    [],
    "Track's httpx.DecodeJSON uses DisallowUnknownFields, so a field it does not declare is a hard " +
      "400 BAD_JSON before any handler logic runs — the call fails on EVERY invocation and the " +
      "best-effort catch makes it look identical to success. Accepted keys were read from " +
      "model.Comment at talyvor-track 7ad05ce3.",
  );
});

test("the accepted-key table is not empty and names the field the fix turned on", () => {
  // ⚠ WITHOUT THIS THE GUARD PASSES BY HAVING NOTHING TO COMPARE AGAINST. An empty allow-set makes
  // every key an offender (loud); an allow-set containing everything makes none (silent). This
  // pins the shape of the table itself.
  const allowed = accepted["issues/{}/comments"];
  assert.ok(allowed.includes("body"), "the table must contain the field the comment route reads");
  assert.ok(!allowed.includes("content"), "`content` is the field that never existed — it must not be listed as accepted");
  assert.ok(allowed.length >= 5, "the transcription looks truncated");
});
