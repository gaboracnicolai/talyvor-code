// safeBaseUrl decides where a Talyvor API key may be sent. It is the TypeScript port of
// agent/internal/safeurl/safeurl.go, and the two are asserted against the same shared cases
// (testdata/safeurl-cases.json) from both runtimes.
//
// ⚠ WHY THIS IS ITS OWN MODULE. It used to live in config.ts, which imports "vscode". The unit
// runner is `node --test out/src/**.test.js`, and requiring a module that imports "vscode" outside
// the editor fails with MODULE_NOT_FOUND — measured, not assumed. So the only guard on where the
// customer's key is sent was untestable BY CONSTRUCTION, and had no test of any kind: the string
// "safeBaseUrl" appeared nowhere in this repo except its own definition and its three call sites.
// secrets-pure.ts, cmdguard-pure.ts and confine-pure.ts were each extracted for exactly this reason;
// this rule was the one that was not.
//
// ⚠ THE TWO PORTS SEE DIFFERENT HOSTS FOR THE SAME URL, which is what made them drift:
//   - Node's URL.hostname KEEPS the IPv6 brackets ("[::1]"); Go's url.Hostname() strips them ("::1").
//     A `host === "::1"` comparison is therefore dead on this side and live on the other.
//   - Node NORMALISES legacy IPv4 spellings (0xa9fea9fe -> 169.254.169.254); Go does not.
// Anything written as a string comparison is blind on one side or the other, so the host is
// normalised HERE, once, before any rule looks at it.

/** hostOf returns the hostname with IPv6 brackets removed, so the rules below compare the same text
 *  Go compares. Returns "" when the URL does not parse. */
function hostOf(u: URL): string {
  const h = u.hostname;
  return h.startsWith("[") && h.endsWith("]") ? h.slice(1, -1) : h;
}

/** ipv6Bytes expands an IPv6 literal to its 16 bytes, or null if it is not one. Written out rather
 *  than regexed because the link-local rules are RANGES (fe80::/10 is fe80–febf) and the previous
 *  implementation used a literal "fe80" prefix, which let fe90:: and ff02:: through. */
function ipv6Bytes(host: string): number[] | null {
  if (!host.includes(":")) return null;
  const halves = host.split("::");
  if (halves.length > 2) return null;
  const toWords = (s: string): number[] | null => {
    if (s === "") return [];
    const out: number[] = [];
    for (const part of s.split(":")) {
      if (!/^[0-9a-fA-F]{1,4}$/.test(part)) return null;
      out.push(parseInt(part, 16));
    }
    return out;
  };
  const head = toWords(halves[0]);
  const tail = halves.length === 2 ? toWords(halves[1]) : [];
  if (head === null || tail === null) return null;
  let words: number[];
  if (halves.length === 2) {
    const fill = 8 - head.length - tail.length;
    if (fill < 0) return null;
    words = [...head, ...new Array(fill).fill(0), ...tail];
  } else {
    if (head.length !== 8) return null;
    words = head;
  }
  const bytes: number[] = [];
  for (const w of words) bytes.push((w >> 8) & 0xff, w & 0xff);
  return bytes;
}

/** ipv4Bytes returns the 4 bytes of a CANONICAL dotted-quad, or null. Canonical means what Go's
 *  net.ParseIP means: exactly four decimal parts, no leading zeros, no hex, no short form. */
function ipv4Bytes(host: string): number[] | null {
  const parts = host.split(".");
  if (parts.length !== 4) return null;
  const out: number[] = [];
  for (const p of parts) {
    if (!/^(0|[1-9][0-9]{0,2})$/.test(p)) return null;
    const n = Number(p);
    if (n > 255) return null;
    out.push(n);
  }
  return out;
}

/** looksNumeric is true for a host made only of the characters a legacy inet_aton address can use
 *  and starting with a digit — 0xa9fea9fe, 2852039166, 0177.0.0.1, 127.1, 169.254.169.254. — but
 *  false for a DNS label that merely starts with a digit (123.example.com) because those carry
 *  letters outside the hex alphabet. Combined with ipv4Bytes below it means: a numeric-looking host
 *  that is not a canonical address is refused rather than handed to a resolver that will interpret
 *  it. MEASURED on this machine: 0xa9fea9fe and 2852039166 both resolve to 169.254.169.254. */
function looksNumeric(host: string): boolean {
  return /^[0-9][0-9a-fA-FxX.]*$/.test(host);
}

/**
 * safeBaseUrl returns raw only if it is a safe Talyvor endpoint, else "". The config is
 * WORKSPACE-scoped, so a hostile repo's .vscode/settings.json could point a URL at an attacker host
 * (with the user's API key attached) or at the cloud metadata endpoint. Require https (except
 * explicit loopback dev), and reject link-local / metadata / unspecified addresses however they are
 * spelled. An unsafe URL sanitizes to "" so the client is never configured with it.
 */
export function safeBaseUrl(raw: string): string {
  if (!raw) return "";
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return "";
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return "";
  const host = hostOf(u);
  if (!host) return "";

  const v4 = ipv4Bytes(host);
  const v6 = ipv6Bytes(host);

  // A numeric-looking host that is not a canonical address means one thing to Node and another to
  // Go. Refuse it on both sides rather than pick a winner. Node has already rewritten the raw text
  // by the time we see it, so the respelling is detected by comparing back against what was typed.
  if (v4 !== null && !raw.toLowerCase().includes(host)) return "";
  if (v4 === null && v6 === null && looksNumeric(host)) return "";

  const isLocal =
    host === "localhost" ||
    host === "127.0.0.1" ||
    host === "::1";
  if (u.protocol !== "https:" && !isLocal) return "";

  if (v4 !== null) {
    // 169.254.0.0/16 link-local (the cloud metadata range), 224.0.0.0/24 link-local multicast,
    // 0.0.0.0 unspecified — the same three families Go's net.IP predicates cover.
    if (v4[0] === 169 && v4[1] === 254) return "";
    if (v4[0] === 224 && v4[1] === 0 && v4[2] === 0) return "";
    if (v4.every((b) => b === 0)) return "";
  }
  if (v6 !== null) {
    const v4mapped =
      v6.slice(0, 10).every((b) => b === 0) && v6[10] === 0xff && v6[11] === 0xff;
    if (v4mapped) {
      if (v6[12] === 169 && v6[13] === 254) return "";
      if (v6[12] === 224 && v6[13] === 0 && v6[14] === 0) return "";
    }
    if (v6[0] === 0xfe && (v6[1] & 0xc0) === 0x80) return ""; // fe80::/10 link-local unicast
    if (v6[0] === 0xff && (v6[1] & 0x0f) === 0x02) return ""; // ffx2:: link-local multicast
    if (v6.every((b) => b === 0)) return ""; // :: unspecified
  }
  return raw;
}
