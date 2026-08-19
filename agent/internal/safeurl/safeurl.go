// Package safeurl is the single definition of "a base URL it is safe to attach a Talyvor
// API key to".
//
// AUDIT FINDING (the shape, not the two instances): this rule lived as an unexported
// helper in internal/config, reachable only through Config.Validate(). That made it
// OPT-IN AT THE CALL SITE — eleven subcommands remembered to call Validate and two did
// not, so `talyvor-code serve` and `talyvor-code init` sent the customer's key in
// cleartext to whatever host was configured. Proven with a canary listener:
//
//	LEAK  POST /v1/proxy/anthropic/v1/messages  Bearer tlv_CANARY_LENS
//
// Fixing those two would have left the thirteenth caller free to forget. So the rule moves
// HERE, and every client constructor (lens/track/docs) applies it and returns an error.
// There is no exported way to build a client that skips it — the guard is now a property
// of construction rather than a step a caller must remember.
package safeurl

import (
	"fmt"
	"net"
	"net/url"
)

// Validate rejects a base URL that would leak an attached API key.
//
// Rules:
//   - must parse, have a host, and be http or https;
//   - must be https UNLESS the host is explicitly loopback (local dev);
//   - must never be a link-local / cloud-metadata / unspecified address, so a hostile
//     config cannot point the client at 169.254.169.254 and collect the key;
//   - must not be an ambiguous numeric host — see isAmbiguousNumericHost. The rule above
//     was keyed on net.ParseIP, which is nil for the legacy inet_aton spellings, so
//     https://0xa9fea9fe/ and https://2852039166/ were ACCEPTED while the dotted spelling
//     of the same address was refused.
//
// This rule is implemented THREE TIMES — here, in extension/src/safeurl-pure.ts, and in
// jetbrains/src/main/kotlin/com/talyvor/code/SafeUrlPure.kt — and all three are asserted
// against the same testdata/safeurl-cases.json from their own runtimes. This comment said
// TWICE until the third was found: it had shipped as a private helper inside the JetBrains
// plugin's TalyvorSettings, so it had no filename of its own for a census to grep for, and
// it disagreed with this table on 8 of the 29 cases — 7 of them FAIL-OPEN, including
// https://0xa9fea9fe/ and every IPv6 link-local address.
//
// The first two disagreed on 6 of those 29 cases before that file existed, in both
// directions, because each was written as string comparisons against the host shape its own
// runtime produces. The Kotlin port made it three shapes: java.net.URI.getHost() keeps the
// IPv6 brackets and normalises nothing.
//
// An EMPTY url is not an error: Track and Docs are optional integrations, and their
// clients report IsConfigured()==false rather than failing a command that never needed
// them. Callers that require a URL check for it separately (Config.Validate still does).
func Validate(name, raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s: invalid URL %q", name, raw)
	}
	host := u.Hostname()
	// A numeric-looking host that is not a canonical address is refused BEFORE anything else looks
	// at it. net.ParseIP is nil for the legacy inet_aton spellings, so the link-local rule below
	// never ran for them — and MEASURED on a Mac, the resolver answers 169.254.169.254 for both
	// "0xa9fea9fe" and "2852039166", and Go's own http.Client reaches a loopback listener through
	// "0x7f000001". So the address this function exists to refuse was reachable by respelling it.
	// Refusing rather than teaching this function inet_aton also keeps it equal to the TypeScript
	// port, where Node has already rewritten such a host by the time the rule sees it.
	if isAmbiguousNumericHost(host) {
		return fmt.Errorf("%s: refusing ambiguous numeric host %q — write the address in its canonical form", name, host)
	}
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !isLocal {
		return fmt.Errorf("%s must be https (got %q) — the API key must not be sent in cleartext", name, raw)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return fmt.Errorf("%s: refusing link-local/metadata host %q", name, host)
	}
	return nil
}

// isAmbiguousNumericHost reports a host made only of the characters a legacy inet_aton address can
// use and starting with a digit — 0xa9fea9fe, 2852039166, 0177.0.0.1, 127.1, 169.254.169.254. — that
// net.ParseIP does not accept as an address. A DNS label may start with a digit (123.example.com),
// but such names carry letters outside the hex alphabet, so they are not caught here.
func isAmbiguousNumericHost(host string) bool {
	if host == "" || host[0] < '0' || host[0] > '9' {
		return false
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F', c == 'x', c == 'X', c == '.':
		default:
			return false
		}
	}
	return net.ParseIP(host) == nil
}
