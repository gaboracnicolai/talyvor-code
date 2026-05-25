// IssueContextProvider — the bridge between editing and Track.
// Holds the currently active TrackIssue, accumulates session cost
// per issue, and pushes that cost back to Track on a timer. The
// rest of the extension reads `getIssueContext()` to enrich AI
// prompts so the model knows what feature is being built.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import { TrackClient, type TrackIssue } from "./client";
import {
  accumulateSessionCost,
  buildAgentCompletionComment,
  formatIssueContext,
  isValidIssueIdentifier,
  pickIssuesForSync,
} from "./issue-context-pure";

export class IssueContextProvider {
  private currentIssue: TrackIssue | undefined;
  // sessionCostByIssue is the rollup we push to Track. It deltas
  // from zero each editor session — Track's stored total is what
  // we add into, then we reset the per-issue bucket so the next
  // sync doesn't double-count.
  private sessionCostByIssue = new Map<string, number>();
  private updateListeners: Array<() => void> = [];

  constructor(
    private readonly trackClient: TrackClient,
    // lensClient is part of the documented constructor signature
    // and kept here for parity with the spec; the cost-attribution
    // flow already goes through Lens via the completion path, so
    // the provider itself doesn't call Lens directly today.
    private readonly _lensClient: LensClient,
  ) {
    void this._lensClient;
  }

  // setActiveIssue fetches the issue from Track, persists it via
  // VS Code settings, and notifies listeners. Returns the fetched
  // issue (or null) so callers can show a confirmation toast.
  // Falls back to a synthetic issue when Track lookup fails so
  // the attribution header still flows.
  async setActiveIssue(
    identifier: string,
    workspaceId: string,
  ): Promise<TrackIssue | null> {
    const id = identifier.trim();
    if (id === "") {
      this.currentIssue = undefined;
      await vscode.workspace
        .getConfiguration("talyvor")
        .update("activeIssue", "", vscode.ConfigurationTarget.Workspace);
      this.notify();
      return null;
    }
    if (!isValidIssueIdentifier(id)) {
      void vscode.window.showWarningMessage(
        `"${id}" doesn't look like a Track issue identifier (e.g. ENG-42). Cost attribution will still work.`,
      );
    }
    const issue = await this.trackClient.getIssue(workspaceId, id);
    if (issue) {
      this.currentIssue = issue;
    } else {
      // Synthetic placeholder — keeps prompts coherent and lets
      // the cost-attribution header still ride.
      this.currentIssue = {
        id: "",
        identifier: id,
        title: id,
        status: "",
        description: "",
        aiCostUsd: 0,
      };
    }
    await vscode.workspace
      .getConfiguration("talyvor")
      .update("activeIssue", id, vscode.ConfigurationTarget.Workspace);
    this.notify();
    return issue;
  }

  getCurrentIssue(): TrackIssue | undefined {
    return this.currentIssue;
  }

  // getIssueContext returns the formatted system-prompt fragment
  // for the currently active issue. Empty string when no issue —
  // callers can concatenate unconditionally.
  getIssueContext(): string {
    return formatIssueContext(this.currentIssue);
  }

  // recordCost is the hook the cost tracker uses to tell us a
  // call completed. We accumulate per issue so the sync timer can
  // PATCH only the issues that actually moved.
  recordCost(issueId: string, costUsd: number): void {
    this.sessionCostByIssue = accumulateSessionCost(
      this.sessionCostByIssue,
      issueId,
      costUsd,
    );
  }

  getSessionCostByIssue(): Map<string, number> {
    return new Map(this.sessionCostByIssue);
  }

  // syncCostToTrack reads the current rollup, GETs each issue's
  // stored cost, adds the session delta, PATCHes back, then
  // zeros the local bucket so we don't double-apply on the next
  // tick. Best-effort throughout — any HTTP failure is swallowed.
  async syncCostToTrack(workspaceId: string): Promise<{ synced: number }> {
    const targets = pickIssuesForSync(this.sessionCostByIssue);
    let synced = 0;
    for (const { issueId, costUsd } of targets) {
      try {
        const issue = await this.trackClient.getIssue(workspaceId, issueId);
        if (!issue || !issue.id) continue;
        const next = issue.aiCostUsd + costUsd;
        await this.trackClient.updateIssueCost(workspaceId, issue.id, next);
        this.sessionCostByIssue.set(issueId, 0);
        synced++;
      } catch {
        // best-effort — leave the bucket so the next tick retries
      }
    }
    return { synced };
  }

  // pushAgentCompletionComment writes the standard "agent task
  // completed" comment to Track. Called from AgentMode after a
  // successful applyApproved.
  async pushAgentCompletionComment(
    workspaceId: string,
    issueIdentifier: string,
    description: string,
    fileCount: number,
    costUsd: number,
    model: string,
  ): Promise<void> {
    if (!workspaceId || !issueIdentifier) return;
    const issue = await this.trackClient.getIssue(workspaceId, issueIdentifier);
    if (!issue || !issue.id) return;
    const body = buildAgentCompletionComment(description, fileCount, costUsd, model);
    await this.trackClient.addComment(workspaceId, issue.id, body);
  }

  // onUpdate / notify is the listener bus used by the status bar
  // and any other UI that wants to react to issue changes.
  onUpdate(fn: () => void): vscode.Disposable {
    this.updateListeners.push(fn);
    return {
      dispose: () => {
        this.updateListeners = this.updateListeners.filter((x) => x !== fn);
      },
    };
  }

  private notify(): void {
    for (const l of this.updateListeners) {
      try {
        l();
      } catch {
        // Listener bugs must never break setActiveIssue.
      }
    }
  }
}
