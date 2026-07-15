package lens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Embed turns texts into embedding vectors by routing through Lens's OpenAI
// embeddings proxy — the SAME gateway, auth, and issue-attribution headers as every
// chat call, so the trust boundary is unchanged (chunk text is sent to Lens exactly
// as chat prompts are; nothing new leaves the machine and no local model is added).
// Vectors are returned ordered by the response's `index`, so callers can zip them
// back to the input order. A no-op on empty input.
func (c *Client) Embed(ctx context.Context, texts []string, model, feature, workspaceID, issueID string) ([][]float32, error) {
	if !c.IsConfigured() {
		return nil, errors.New("lens: not configured")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	enc, err := json.Marshal(map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/proxy/openai/v1/embeddings", bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Talyvor-Feature", "code-"+feature)
	req.Header.Set("X-Talyvor-Workspace", workspaceID)
	req.Header.Set("X-Talyvor-Issue", issueID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("lens: embeddings: %s", resp.Status)
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		idx := d.Index
		if idx < 0 || idx >= len(vecs) {
			idx = i // fall back to arrival order if the server omits/!bounds index
		}
		vecs[idx] = d.Embedding
	}
	return vecs, nil
}
