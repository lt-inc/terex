// Package llm talks to OpenRouter: embeddings for dedup/clustering/relevance and chat
// completions for post generation.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"terex/news-collector/internal/metrics"
)

type Client struct {
	BaseURL        string // default https://openrouter.ai/api/v1
	APIKey         string
	Model          string // default chat model
	EmbeddingModel string
	HTTP           *http.Client
}

func New(apiKey, model, embeddingModel string) *Client {
	return &Client{BaseURL: "https://openrouter.ai/api/v1", APIKey: apiKey, Model: model, EmbeddingModel: embeddingModel, HTTP: &http.Client{Timeout: 4 * time.Minute}}
}

func (c *Client) Ready() bool { return c != nil && c.APIKey != "" }

var ErrNotConfigured = errors.New("OPENROUTER_API_KEY is not configured")

type apiError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) post(ctx context.Context, path string, in, out any) (err error) {
	if !c.Ready() {
		return ErrNotConfigured
	}
	kind := "chat"
	if path == "/embeddings" {
		kind = "embedding"
	}
	started := time.Now()
	defer func() {
		result := "ok"
		if err != nil {
			result = "error"
		}
		metrics.LLMRequests.WithLabelValues(kind, result).Inc()
		metrics.LLMDuration.WithLabelValues(kind).Observe(time.Since(started).Seconds())
	}()
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.HTTP.Do(req)
		if err != nil {
			last = fmt.Errorf("openrouter unavailable")
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		res.Body.Close()
		if res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500 {
			last = fmt.Errorf("openrouter HTTP %d", res.StatusCode)
			continue
		}
		if res.StatusCode != http.StatusOK {
			var e apiError
			_ = json.Unmarshal(data, &e)
			if e.Error.Message == "" {
				e.Error.Message = fmt.Sprintf("HTTP %d", res.StatusCode)
			}
			return fmt.Errorf("openrouter: %s", e.Error.Message)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			last = fmt.Errorf("openrouter returned an empty response")
			continue
		}
		if err = json.Unmarshal(data, out); err != nil {
			last = fmt.Errorf("openrouter returned invalid JSON")
			continue
		}
		return nil
	}
	return last
}

// Embed returns one vector per input text, in order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/embeddings", map[string]any{"model": c.EmbeddingModel, "input": texts}, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("openrouter returned %d embeddings for %d texts", len(out.Data), len(texts))
	}
	vectors := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(texts) || len(d.Embedding) == 0 {
			return nil, fmt.Errorf("openrouter returned an invalid embedding")
		}
		vectors[d.Index] = d.Embedding
	}
	for _, v := range vectors {
		if v == nil {
			return nil, fmt.Errorf("openrouter skipped an embedding")
		}
	}
	return vectors, nil
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// JSON runs a chat completion in JSON mode and decodes the answer into out.
func (c *Client) JSON(ctx context.Context, model string, messages []Message, out any) error {
	if model == "" {
		model = c.Model
	}
	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	req := map[string]any{
		"model":           model,
		"messages":        messages,
		"temperature":     0.4,
		"response_format": map[string]string{"type": "json_object"},
	}
	if err := c.post(ctx, "/chat/completions", req, &res); err != nil {
		return err
	}
	if len(res.Choices) == 0 {
		return fmt.Errorf("openrouter returned no choices")
	}
	content := strings.TrimSpace(res.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), out); err != nil {
		return fmt.Errorf("model answer is not valid JSON")
	}
	return nil
}
