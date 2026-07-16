package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/talyvor/code/internal/agentloop"
	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// lensModel adapts the Lens client to agentloop.Model. Every loop turn is a Lens
// completion carrying the SAME issue/workspace attribution headers as any other
// agent call (feature "agent-loop") — the moat is preserved; nothing new leaves the
// machine beyond the model call that already happens.
//
// It also implements agentloop.OutputIdentified: it records the gateway-bound output_id
// (X-Talyvor-Output-Id) of its most recent completion so the loop can attribute K4
// mechanical verdicts to the exact generation that produced a code change. Pointer
// receiver — the single-threaded loop calls Complete then reads LastOutputID in order.
type lensModel struct {
	lc           *lens.Client
	model        string
	workspaceID  string
	issueID      string
	lastOutputID string
}

func newLensModel(lc *lens.Client, model, workspaceID, issueID string) *lensModel {
	return &lensModel{lc: lc, model: model, workspaceID: workspaceID, issueID: issueID}
}

func (m *lensModel) Complete(ctx context.Context, messages []agentloop.Message) (string, error) {
	lm := make([]lens.Message, len(messages))
	for i, msg := range messages {
		lm[i] = lens.Message{Role: msg.Role, Content: msg.Content}
	}
	// CompleteWithUsage (vs Complete) so we can capture the output_id from the response
	// header — the only added coupling; the request is byte-identical to before.
	usage, err := m.lc.CompleteWithUsage(ctx, lm, m.model, "agent-loop", m.workspaceID, m.issueID)
	if err != nil {
		m.lastOutputID = ""
		return "", err
	}
	m.lastOutputID = usage.OutputID
	return usage.Text, nil
}

// LastOutputID returns the output_id of the most recent completion (satisfies
// agentloop.OutputIdentified); "" when the gateway returned none.
func (m *lensModel) LastOutputID() string { return m.lastOutputID }

// verdictObserverFor returns the loop's Observer when K4 verdict reporting is enabled,
// else nil — so with ReportVerdicts OFF the loop's Observer stays nil and the loop is
// byte-identical to before. Returning an untyped nil (not a nil *verdictObserver) keeps
// the interface truly nil.
func verdictObserverFor(ctx context.Context, cfg config.Config, lc *lens.Client, log io.Writer) agentloop.Observer {
	if !cfg.ReportVerdicts {
		return nil
	}
	return newVerdictObserver(ctx, lc, log)
}

// runIterativeAgent drives the tool-using loop for a task: it builds the confined
// tool set (search/read/edit/run) over the repo root + the semantic retriever, wires
// the Lens model, runs the OBSERVE/ACT loop, and prints the outcome.
func runIterativeAgent(ctx context.Context, lc *lens.Client, cfg config.Config, task, root, model string, ret codebase.Retriever, maxSteps int, stdout, stderr io.Writer) error {
	tools := agentloop.DefaultTools(root, ret)
	m := newLensModel(lc, model, cfg.WorkspaceID, cfg.ActiveIssue)
	// K4: attribute mechanical build/test verdicts to the generation that produced each
	// edit (flag-gated, best-effort). Observer is nil when ReportVerdicts is off ⇒ the
	// loop runs exactly as before.
	ag := agentloop.New(m, tools, agentloop.Config{MaxSteps: maxSteps, Observer: verdictObserverFor(ctx, cfg, lc, stderr)})

	fmt.Fprintf(stdout, "▸ Iterative agent — model=%s, max %d steps, tools: %s\n", model, maxSteps, strings.Join(tools.Names(), ", "))
	if ret == nil {
		fmt.Fprintln(stdout, "  (no semantic index — search_codebase is note-only; run `talyvor-code index` for retrieval)")
	}

	res, err := ag.Run(ctx, task)
	if err != nil {
		return fmt.Errorf("iterative agent: %w", err)
	}

	fmt.Fprintf(stdout, "\n■ %s after %d step(s).\n", res.Stop, res.Steps)
	if res.Summary != "" {
		fmt.Fprintf(stdout, "Summary: %s\n", res.Summary)
	}
	if len(res.EditedFiles) > 0 {
		fmt.Fprintf(stdout, "Edited: %s\n", strings.Join(res.EditedFiles, ", "))
	}
	if !res.Done {
		fmt.Fprintf(stderr, "! agent did not reach a clean done (%s) — review the changes above\n", res.Stop)
	}
	fmt.Fprintf(stderr, "(model=%s issue=%s)\n", model, nonEmpty(cfg.ActiveIssue, "(none)"))
	return nil
}
