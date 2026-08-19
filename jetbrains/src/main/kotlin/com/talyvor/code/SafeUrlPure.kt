// THE THIRD PORT OF "a base URL it is safe to attach a Talyvor API key to".
//
// The rule was written three times, and the two files that carry it said so twice — wrongly:
// agent/internal/safeurl/safeurl.go's package doc says "This rule is implemented TWICE — here and in
// extension/src/safeurl-pure.ts", and testdata/safeurl-cases.json opens with "It is implemented
// TWICE". This file is the third implementation, it ships in the JetBrains plugin, it guards the same
// thing in its own words ("so a hostile config can't make the client send the API key to an attacker
// or internal host"), and it was in NO parity harness and had NO test of any kind.
//
// It lived as a private helper inside TalyvorSettings' companion object, which is why it was easy to
// miss: a census that greps for the rule's own filename cannot see a rule with no file. It is lifted
// out here — free of the IntelliJ Platform, so a plain JUnit test can reach it — and TalyvorSettings
// now delegates. Delegation rather than a copy: SafeUrlParityTest asserts the code the plugin runs,
// not a second transcription of it.
//
// The host normalisation is a THIRD one again, and that is the whole reason the ports drifted. Go's
// url.Hostname() strips the IPv6 brackets and leaves legacy IPv4 spellings alone; Node's URL.hostname
// keeps the brackets and rewrites 0xa9fea9fe to 169.254.169.254; java.net.URI.getHost() KEEPS the
// brackets and normalises nothing. Every port was then written as string comparisons against the
// shape its own runtime produces, so each is blind exactly where the others are not.

package com.talyvor.code

import java.net.InetAddress
import java.net.URI

object SafeUrlPure {

    // sanitizeBaseUrl returns the URL when it is safe to attach an API key to, and "" when it is not.
    // "" is this port's spelling of "refused" — the settings getters hand it to a client that then
    // reports itself unconfigured, the same meaning safeurl.Validate spells as an error and
    // safeBaseUrl spells as "".
    //
    // The verdicts are pinned against testdata/safeurl-cases.json, the same file the Go and
    // TypeScript ports assert against, by SafeUrlParityTest.
    fun sanitizeBaseUrl(raw: String): String {
        if (raw.isBlank()) return ""
        val u = try {
            URI(raw)
        } catch (e: Exception) {
            return ""
        }
        if (u.scheme != "http" && u.scheme != "https") return ""
        // java.net.URI.getHost() KEEPS the IPv6 brackets, so every comparison below used to be
        // against "[fe80::1]" rather than "fe80::1". That single normalisation is where five of the
        // eight measured disagreements came from: `host == "::1"` could never be true (IPv6 loopback
        // was refused for local dev) and `host.startsWith("fe80")` could never be true (every IPv6
        // link-local was ACCEPTED, with the API key attached).
        val host = stripIPv6Brackets(u.host ?: return "")

        // Refused BEFORE anything else looks at it, mirroring the Go port. A numeric-looking host
        // that is not a canonical address is not resolved here and not reasoned about — it is
        // refused, because the address this function exists to keep a key away from is reachable by
        // respelling it: 0xa9fea9fe and 2852039166 are both 169.254.169.254, and the string rules
        // below see neither.
        if (isAmbiguousNumericHost(host)) return ""

        val isLocal = host == "localhost" || host == "127.0.0.1" || host == "::1"
        if (u.scheme != "https" && !isLocal) return ""

        // Link-local / metadata / unspecified by ADDRESS SEMANTICS, not by string prefix. The old
        // `startsWith("fe80")` was wrong twice over: fe80::/10 runs to febf (so fe90::1 is link-local
        // and did not match) and link-local MULTICAST (ff02::1) is a different prefix entirely. This
        // is the Go port's `IsLinkLocalUnicast() || IsLinkLocalMulticast() || IsUnspecified()`.
        val ip = parseLiteralIP(host)
        if (ip != null && (ip.isLinkLocalAddress || ip.isMCLinkLocal || ip.isAnyLocalAddress)) return ""
        return raw
    }

    // stripIPv6Brackets turns the "[::1]" java.net.URI hands back into the "::1" every rule here is
    // written against. Go's url.Hostname() already does this; Node's URL.hostname does not, and the
    // TypeScript port carries its own version of this same normalisation for the same reason.
    private fun stripIPv6Brackets(host: String): String =
        if (host.length >= 2 && host.startsWith("[") && host.endsWith("]")) host.substring(1, host.length - 1) else host

    // parseLiteralIP parses a host that is ALREADY an IP literal and NEVER performs a DNS lookup: it
    // is called only for a strict dotted-quad or for a string containing ':', neither of which can be
    // a DNS name. This matters — InetAddress.getByName() on a hostname would resolve it, turning a
    // safety check into a network request driven by hostile config. (Java 17 has no parse-only
    // public API; InetAddress.ofLiteral arrived later.)
    private fun parseLiteralIP(host: String): InetAddress? {
        val looksIPv6 = host.contains(':')
        if (!looksIPv6 && !isCanonicalIPv4(host)) return null
        return try {
            InetAddress.getByName(host)
        } catch (e: Exception) {
            null
        }
    }

    // isCanonicalIPv4 accepts only the dotted-quad spelling, with no leading zeros — the same set
    // Go's net.ParseIP accepts, so "0177.0.0.1" is NOT an address here either and falls to the
    // ambiguous-numeric refusal above rather than being normalised into 127.0.0.1.
    private fun isCanonicalIPv4(host: String): Boolean {
        val parts = host.split('.')
        if (parts.size != 4) return false
        for (p in parts) {
            if (p.isEmpty() || p.length > 3) return false
            if (!p.all { it in '0'..'9' }) return false
            if (p.length > 1 && p[0] == '0') return false
            if (p.toInt() > 255) return false
        }
        return true
    }

    // isAmbiguousNumericHost reports a host made only of the characters a legacy inet_aton address
    // can use and starting with a digit — 0xa9fea9fe, 2852039166, 0177.0.0.1, 127.1, 169.254.169.254.
    // — that is not a canonical dotted-quad. A DNS label may start with a digit (123.example.com),
    // but such names carry letters outside the hex alphabet, so they are not caught here. Character
    // for character the Go port's function of the same name.
    private fun isAmbiguousNumericHost(host: String): Boolean {
        if (host.isEmpty() || host[0] !in '0'..'9') return false
        for (c in host) {
            val ok = c in '0'..'9' || c in 'a'..'f' || c in 'A'..'F' || c == 'x' || c == 'X' || c == '.'
            if (!ok) return false
        }
        return !isCanonicalIPv4(host)
    }
}
