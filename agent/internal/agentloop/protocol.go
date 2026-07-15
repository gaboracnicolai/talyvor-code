package agentloop

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolCall is one parsed model turn.
type ToolCall struct {
	Thought string          `json:"thought"`
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
}

// DESIGN FORK (tool-call transport): the model expresses each action as a single
// JSON object in its text reply, which we parse and dispatch — rather than the
// providers' NATIVE tool-calling (Anthropic tool_use / OpenAI functions). Chosen
// for: provider-agnosticism (works through the existing lens.Complete unchanged, no
// new client surface), reuse of the repo's established "ask for JSON, parse
// defensively" pattern (parsePlan / ParseHealResult), and deterministic testing
// with a scripted model stub. ALTERNATIVE to revisit: native tool-calling gives the
// model a structured tools API and removes JSON-format brittleness, at the cost of a
// per-provider client change and harder stubbing. See BUILD_STATE.md.

// parseToolCall extracts a ToolCall from a model reply. Defensive against the usual
// ways models wrap JSON: ```json fences and leading/trailing prose.
func parseToolCall(reply string) (ToolCall, error) {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = strings.TrimRight(s[:j], "\n")
		}
		s = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "{") {
		if a := strings.Index(s, "{"); a >= 0 {
			if b := strings.LastIndex(s, "}"); b > a {
				s = s[a : b+1]
			}
		}
	}
	var tc ToolCall
	if err := json.Unmarshal([]byte(s), &tc); err != nil {
		return ToolCall{}, fmt.Errorf("reply is not a JSON tool call: %w", err)
	}
	if strings.TrimSpace(tc.Tool) == "" {
		return ToolCall{}, fmt.Errorf(`reply has no "tool" field`)
	}
	return tc, nil
}

// doneSummary pulls the summary out of a done call's args (best-effort).
func doneSummary(args json.RawMessage) string {
	var a struct {
		Summary string `json:"summary"`
	}
	_ = json.Unmarshal(args, &a)
	return a.Summary
}

// editPath pulls the path out of an edit_file call's args (for the changed-files list).
func editPath(args json.RawMessage) string {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &a)
	return a.Path
}

// systemPrompt describes the tools + the one-JSON-object-per-turn protocol.
func systemPrompt(reg Registry) string {
	var b strings.Builder
	b.WriteString("You are an ITERATIVE coding agent. Accomplish the task by calling ONE tool per turn, ")
	b.WriteString("observing its result, and deciding the next call — searching, reading, editing, and running ")
	b.WriteString("until the task is complete AND verified.\n\nTools:\n")
	for _, name := range reg.Names() {
		fmt.Fprintf(&b, "- %s\n", reg[name].Description())
	}
	b.WriteString(`- done {"summary":"what you did"} — finish ONLY when the task is complete and the build/tests pass.` + "\n\n")
	b.WriteString("Respond with EXACTLY ONE JSON object and nothing else:\n")
	b.WriteString(`{"thought":"brief reasoning","tool":"<tool>","args":{...}}` + "\n\n")
	b.WriteString("Ground every edit in what you READ/SEARCHED/RAN. After changing code, RUN the build/tests, ")
	b.WriteString("read the failure, and fix it — never repeat an identical edit. Finish with the done tool.\n")
	return b.String()
}
