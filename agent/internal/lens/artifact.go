package lens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ManifestEntry is one file of the buildable module in an H5 artifact commitment: its module-relative
// forward-slash path and the hex sha256 of its content. Bytes never travel — hashes only. Mirrors
// talyvor-lens outputverify.ManifestEntry (merged @ 5b0b3d1).
type ManifestEntry struct {
	Path          string `json:"path"`
	ContentSHA256 string `json:"content_sha256"`
}

// ErrArtifactNoContentBinding is Lens's 409: the output has NO output_content_sha256 (captured before the
// content binding, extraction failed, or a stream) — an artifact commitment for it is PERMANENTLY
// impossible. The caller logs it and moves on; it must never fail the PR.
var ErrArtifactNoContentBinding = errors.New("lens: output has no content binding; artifact commitment impossible")

// CommitArtifact opts the PRODUCING workspace's output in to the H5 buildable-artifact commitment via
// POST /v1/outputs/{output_id}/artifact {output_path, context_manifest}. Client-side, NO authority: Lens
// owns the gate (owner-bound, append-once, and it FOLDS the captured output_content_sha256 into the slot —
// any caller-supplied slot hash is ignored). Returns committed=true on the first commit; (false, nil) on an
// append-once repeat (200 committed:false). A 409 returns ErrArtifactNoContentBinding for the caller to
// log. A no-op when unconfigured or output_id is empty. Best-effort: every failure is returned for the
// caller to swallow — committing must never break a PR.
func (c *Client) CommitArtifact(ctx context.Context, outputID, outputPath string, contextManifest []ManifestEntry) (bool, error) {
	if !c.IsConfigured() || outputID == "" {
		return false, nil
	}
	if contextManifest == nil {
		contextManifest = []ManifestEntry{} // encode as [], never null
	}
	enc, err := json.Marshal(map[string]any{
		"output_path":      outputPath,
		"context_manifest": contextManifest,
	})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/outputs/"+outputID+"/artifact", bytes.NewReader(enc))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return false, ErrArtifactNoContentBinding
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("lens: commit artifact: %s", resp.Status)
	}
	var out struct {
		Committed bool `json:"committed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Committed, nil
}
