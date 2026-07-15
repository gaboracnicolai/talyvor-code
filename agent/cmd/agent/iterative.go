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
type lensModel struct {
	lc          *lens.Client
	model       string
	workspaceID string
	issueID     string
}

func (m lensModel) Complete(ctx context.Context, messages []agentloop.Message) (string, error) {
	lm := make([]lens.Message, len(messages))
	for i, msg := range messages {
		lm[i] = lens.Message{Role: msg.Role, Content: msg.Content}
	}
	return m.lc.Complete(ctx, lm, m.model, "agent-loop", m.workspaceID, m.issueID)
}

// runIterativeAgent drives the tool-using loop for a task: it builds the confined
// tool set (search/read/edit/run) over the repo root + the semantic retriever, wires
// the Lens model, runs the OBSERVE/ACT loop, and prints the outcome.
func runIterativeAgent(ctx context.Context, lc *lens.Client, cfg config.Config, task, root, model string, ret codebase.Retriever, maxSteps int, stdout, stderr io.Writer) error {
	tools := agentloop.DefaultTools(root, ret)
	m := lensModel{lc: lc, model: model, workspaceID: cfg.WorkspaceID, issueID: cfg.ActiveIssue}
	ag := agentloop.New(m, tools, agentloop.Config{MaxSteps: maxSteps})

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
