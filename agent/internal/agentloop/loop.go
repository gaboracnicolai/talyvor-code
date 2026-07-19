package agentloop

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/talyvor/code/internal/lens"
)

// StepInfo is what the loop observed after executing one turn's tool call. An optional
// Observer receives it so an EXTERNAL consumer (K4 verdict reporting) can pair a
// generation to a build/test outcome WITHOUT this package depending on lens/verdicts.
// For a `run` tool that executed, RunExit + Ran carry the structured exit code (no
// observation-string parsing).
type StepInfo struct {
	OutputID string          // the generation's output_id ("" if unknown)
	Tool     string          // tool name dispatched this step
	Args     json.RawMessage // the tool call args
	ToolErr  bool            // the tool returned an error (the observation is the error)
	RunExit  int             // exit code, valid only when Ran
	Ran      bool            // true iff a `run` tool actually executed a command this step
}

// Observer receives one StepInfo per executed tool call. Best-effort by contract: it
// MUST NOT block or panic the loop (a verdict report is fire-and-forget). Nil = no-op.
type Observer interface {
	ObserveStep(StepInfo)
}

// lastRunner is the optional capability the loop reads to obtain a run tool's structured
// exit code after dispatch.
type lastRunner interface {
	LastRun() (exitCode int, ran bool)
}

// StopReason is why the loop ended.
type StopReason int

const (
	StopDone       StopReason = iota // model called done
	StopBudget                       // step budget exhausted
	StopNoProgress                   // the same tool call recurred (edit→fail→identical-edit) — overnight safety
	StopError                        // the model call itself errored
)

func (s StopReason) String() string {
	switch s {
	case StopDone:
		return "done"
	case StopBudget:
		return "budget-exhausted"
	case StopNoProgress:
		return "no-progress"
	case StopError:
		return "model-error"
	default:
		return "unknown"
	}
}

// Config tunes the loop. Zero fields fall back to safe defaults.
type Config struct {
	MaxSteps      int // hard cap on tool-call turns (default 20)
	MaxRepeat     int // abort after an identical tool call recurs more than this (default 2)
	MaxTranscript int // messages kept in context before trimming the oldest (default 40)
	// Observer (optional) receives one StepInfo per executed tool call — used to attach
	// K4 verdict reporting externally. Nil ⇒ the loop behaves exactly as before.
	Observer Observer
}

// Result is the loop outcome.
type Result struct {
	Done        bool
	Summary     string
	Steps       int
	Stop        StopReason
	EditedFiles []string
	// EditAttribution maps a repo-relative file path to the output_id of the LAST
	// generation that wrote it (edit_file writes complete content, so the last writer
	// is the one whose content survives on disk). Empty when the Model exposes no
	// output_id. The attribution caller further filters this by the COMMITTED diff, so
	// a file later reverted to its base content is not attributed.
	EditAttribution map[string]string
	// OutputCanonicalSHA maps an output_id to the sha256 of that generation's CANONICAL reply text —
	// the value Lens captured as output_content_sha256 (lens/canonical.go). Recorded at the only moment
	// the reply and its output_id are paired (the transcript is trimmed, so post-hoc pairing is
	// impossible). The H5 artifact-commit rule compares this against the on-disk file's hash: commit
	// iff equal — for tool-call replies they essentially never are, and the rule correctly declines.
	OutputCanonicalSHA map[string]string
	Transcript         []Message
}

// Agent drives the iterative loop over a Model + a tool Registry.
type Agent struct {
	model Model
	tools Registry
	cfg   Config
}

// New builds an Agent with defaults applied.
func New(m Model, tools Registry, cfg Config) *Agent {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 20
	}
	if cfg.MaxRepeat <= 0 {
		cfg.MaxRepeat = 2
	}
	if cfg.MaxTranscript <= 0 {
		cfg.MaxTranscript = 40
	}
	return &Agent{model: m, tools: tools, cfg: cfg}
}

// Run executes the OBSERVE/ACT loop: the model picks a tool, the tool runs, its
// result is fed back as an observation, and the model re-plans — until done, the
// step budget is hit, or the no-progress detector trips. Self-heal is native: a
// failing `run` is just another observation the model re-plans on.
func (a *Agent) Run(ctx context.Context, task string) (Result, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt(a.tools)},
		{Role: "user", Content: "Task: " + task + "\n\nBegin. Respond with ONE JSON tool call."},
	}
	editedFiles := []string{}
	editAttribution := map[string]string{} // repo-rel path → last-writer output_id
	outputCanonical := map[string]string{} // output_id → sha256(canonical reply text)
	sigCount := map[string]int{}

	for step := 1; step <= a.cfg.MaxSteps; step++ {
		reply, err := a.model.Complete(ctx, messages)
		if err != nil {
			return Result{Stop: StopError, Steps: step, EditedFiles: editedFiles, EditAttribution: editAttribution, OutputCanonicalSHA: outputCanonical, Transcript: messages}, err
		}
		// The output_id of the generation that produced THIS turn's reply (when the Model
		// exposes it). Read immediately after Complete so it pairs 1:1 with this turn.
		var outputID string
		if oi, ok := a.model.(OutputIdentified); ok {
			outputID = oi.LastOutputID()
		}
		if outputID != "" {
			outputCanonical[outputID] = lens.CanonicalContentSHA256(reply)
		}

		call, perr := parseToolCall(reply)
		if perr != nil {
			// Bad format: feed it back so the model can correct — but bound the
			// number of consecutive malformed replies so it can't loop on garbage.
			if sigCount["\x00parse"]++; sigCount["\x00parse"] > a.cfg.MaxRepeat {
				return Result{Stop: StopNoProgress, Steps: step, EditedFiles: editedFiles, EditAttribution: editAttribution, OutputCanonicalSHA: outputCanonical, Transcript: messages}, nil
			}
			messages = a.appendTurn(messages, reply,
				`[error] `+perr.Error()+` — reply with EXACTLY ONE JSON object: {"thought":"...","tool":"<tool>","args":{...}}`)
			continue
		}

		if call.Tool == "done" {
			return Result{Done: true, Summary: doneSummary(call.Args), Steps: step, Stop: StopDone, EditedFiles: editedFiles, EditAttribution: editAttribution, OutputCanonicalSHA: outputCanonical, Transcript: messages}, nil
		}

		// No-progress detector: the identical (tool, args) recurring more than
		// MaxRepeat times means the agent is spinning (the classic
		// edit→fail→identical-edit cycle). Abort BEFORE the step budget so an
		// unattended run can't burn its budget looping.
		sig := call.Tool + "\x00" + string(call.Args)
		if sigCount[sig]++; sigCount[sig] > a.cfg.MaxRepeat {
			return Result{Stop: StopNoProgress, Steps: step, EditedFiles: editedFiles, EditAttribution: editAttribution, OutputCanonicalSHA: outputCanonical, Transcript: messages}, nil
		}

		obs, terr := a.tools.Dispatch(ctx, call.Tool, call.Args)
		if terr != nil {
			obs = "[error] " + terr.Error() // a tool error is an observation; the model re-plans
		} else if call.Tool == "edit_file" {
			p := editPath(call.Args)
			editedFiles = appendUnique(editedFiles, p)
			// Record the last-writer output_id for this file (overwrites an earlier
			// generation's write of the same file — supersession). Skip unknown ids.
			if p != "" && outputID != "" {
				editAttribution[p] = outputID
			}
		}
		// Emit this step to the Observer (if any). For a successfully-executed `run`, read
		// the structured exit code from the tool. Best-effort: nil Observer ⇒ no-op.
		if a.cfg.Observer != nil {
			si := StepInfo{OutputID: outputID, Tool: call.Tool, Args: call.Args, ToolErr: terr != nil}
			if terr == nil {
				if lr, ok := a.tools[call.Tool].(lastRunner); ok {
					si.RunExit, si.Ran = lr.LastRun()
				}
			}
			a.cfg.Observer.ObserveStep(si)
		}
		messages = a.appendTurn(messages, reply, fmt.Sprintf("[%s]\n%s", call.Tool, obs))
	}
	return Result{Stop: StopBudget, Steps: a.cfg.MaxSteps, EditedFiles: editedFiles, EditAttribution: editAttribution, OutputCanonicalSHA: outputCanonical, Transcript: messages}, nil
}

// appendTurn records the assistant reply + the observation, trimming the transcript
// to the context cap (keeping the system message).
func (a *Agent) appendTurn(messages []Message, assistantReply, observation string) []Message {
	messages = append(messages,
		Message{Role: "assistant", Content: assistantReply},
		Message{Role: "user", Content: observation})
	return trimTranscript(messages, a.cfg.MaxTranscript)
}

// trimTranscript keeps the system message + the most recent (max-1) turns.
func trimTranscript(msgs []Message, max int) []Message {
	if len(msgs) <= max {
		return msgs
	}
	out := make([]Message, 0, max)
	out = append(out, msgs[0])
	out = append(out, msgs[len(msgs)-(max-1):]...)
	return out
}

func appendUnique(xs []string, s string) []string {
	if s == "" {
		return xs
	}
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}
