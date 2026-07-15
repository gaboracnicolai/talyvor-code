package agentloop

import (
	"context"
	"fmt"
)

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
}

// Result is the loop outcome.
type Result struct {
	Done        bool
	Summary     string
	Steps       int
	Stop        StopReason
	EditedFiles []string
	Transcript  []Message
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
	sigCount := map[string]int{}

	for step := 1; step <= a.cfg.MaxSteps; step++ {
		reply, err := a.model.Complete(ctx, messages)
		if err != nil {
			return Result{Stop: StopError, Steps: step, EditedFiles: editedFiles, Transcript: messages}, err
		}

		call, perr := parseToolCall(reply)
		if perr != nil {
			// Bad format: feed it back so the model can correct — but bound the
			// number of consecutive malformed replies so it can't loop on garbage.
			if sigCount["\x00parse"]++; sigCount["\x00parse"] > a.cfg.MaxRepeat {
				return Result{Stop: StopNoProgress, Steps: step, EditedFiles: editedFiles, Transcript: messages}, nil
			}
			messages = a.appendTurn(messages, reply,
				`[error] `+perr.Error()+` — reply with EXACTLY ONE JSON object: {"thought":"...","tool":"<tool>","args":{...}}`)
			continue
		}

		if call.Tool == "done" {
			return Result{Done: true, Summary: doneSummary(call.Args), Steps: step, Stop: StopDone, EditedFiles: editedFiles, Transcript: messages}, nil
		}

		// No-progress detector: the identical (tool, args) recurring more than
		// MaxRepeat times means the agent is spinning (the classic
		// edit→fail→identical-edit cycle). Abort BEFORE the step budget so an
		// unattended run can't burn its budget looping.
		sig := call.Tool + "\x00" + string(call.Args)
		if sigCount[sig]++; sigCount[sig] > a.cfg.MaxRepeat {
			return Result{Stop: StopNoProgress, Steps: step, EditedFiles: editedFiles, Transcript: messages}, nil
		}

		obs, terr := a.tools.Dispatch(ctx, call.Tool, call.Args)
		if terr != nil {
			obs = "[error] " + terr.Error() // a tool error is an observation; the model re-plans
		} else if call.Tool == "edit_file" {
			editedFiles = appendUnique(editedFiles, editPath(call.Args))
		}
		messages = a.appendTurn(messages, reply, fmt.Sprintf("[%s]\n%s", call.Tool, obs))
	}
	return Result{Stop: StopBudget, Steps: a.cfg.MaxSteps, EditedFiles: editedFiles, Transcript: messages}, nil
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
