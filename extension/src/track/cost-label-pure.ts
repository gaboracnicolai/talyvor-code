// cost-label-pure.ts — how the status bar renders the local cost estimate.
//
// ⚠ WHY THIS IS ITS OWN MODULE. The number it produces sits directly beside an ISSUE IDENTIFIER in
// the status bar, and Track shows a number for that same issue. They are DIFFERENT QUANTITIES:
//   · this one is a LOCAL estimate of THIS EDITOR SESSION, priced at one flat rate — Claude 3
//     Haiku's, so it is 4x low on claude-haiku-4-5, the model this extension defaults to, and lower
//     still on everything else (the factors live once, in providers/cost-tracker.ts);
//   · Track's is the authoritative per-request cost for the WHOLE issue, recorded by Lens.
// A reader has every reason to take them for the same thing. The "~" and the wording below are the
// only things stopping an understated figure from reading as the bill, which is why they are pinned
// by a test rather than left to a code comment.

/** The status-bar figure. The tilde is load-bearing: it marks the number as approximate. */
export function formatSessionCost(usd: number): string {
  return `~$${usd.toFixed(2)}`;
}

/** The tooltip lines that say what the number is and is not. */
export function costDisclaimerLines(activeIssue?: string): string[] {
  const lines = ["Estimated locally at one flat rate — not your bill."];
  lines.push(
    activeIssue
      ? `Track shows the real cost for ${activeIssue}, recorded by Lens.`
      : "Track shows the real per-issue cost, recorded by Lens.",
  );
  return lines;
}
