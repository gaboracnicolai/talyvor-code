package mcp

import "testing"

// The MCP bearer token is memory-only and rotated by restart (unset/reset TALYVOR_MCP_TOKEN, or just
// relaunch when it is auto-generated). We deliberately do NOT add live rotation — a control endpoint
// would be new attack surface on a single-user loopback server. The one real hole is a NON-loopback
// bind (--host 0.0.0.0) with an auto-generated, printed-once token exposed to the LAN. ResolveServeToken
// closes it: a non-loopback bind REQUIRES an explicit operator-set token (which the operator can rotate
// deliberately), while loopback keeps the convenient auto-generate.
func TestResolveServeToken(t *testing.T) {
	// Explicit token: honoured on any host, never "generated".
	for _, host := range []string{"127.0.0.1", "0.0.0.0", "192.168.1.5"} {
		tok, gen, err := ResolveServeToken("op-secret", host)
		if err != nil || tok != "op-secret" || gen {
			t.Errorf("explicit token on %s: got (%q,%v,%v), want (op-secret,false,nil)", host, tok, gen, err)
		}
	}

	// Loopback + no token: auto-generate (64-hex), generated=true.
	tok, gen, err := ResolveServeToken("", "127.0.0.1")
	if err != nil || !gen || len(tok) != 64 {
		t.Errorf("loopback auto-gen: got (len=%d,gen=%v,err=%v), want (64,true,nil)", len(tok), gen, err)
	}
	if tok2, _, _ := ResolveServeToken("", "localhost"); len(tok2) != 64 {
		t.Errorf("localhost must also auto-generate; got len=%d", len(tok2))
	}

	// Non-loopback + no token: REFUSE (fail closed) — no ephemeral secret exposed to the network.
	for _, host := range []string{"0.0.0.0", "192.168.1.5", "10.0.0.2"} {
		if _, _, err := ResolveServeToken("", host); err == nil {
			t.Errorf("non-loopback %s without an explicit token must error (fail closed)", host)
		}
	}
}
