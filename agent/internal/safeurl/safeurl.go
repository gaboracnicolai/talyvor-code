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
// Rules, unchanged from the original internal/config implementation:
//   - must parse, have a host, and be http or https;
//   - must be https UNLESS the host is explicitly loopback (local dev);
//   - must never be a link-local / cloud-metadata / unspecified address, so a hostile
//     config cannot point the client at 169.254.169.254 and collect the key.
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
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !isLocal {
		return fmt.Errorf("%s must be https (got %q) — the API key must not be sent in cleartext", name, raw)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return fmt.Errorf("%s: refusing link-local/metadata host %q", name, host)
	}
	return nil
}
