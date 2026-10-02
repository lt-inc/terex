package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/proto/telemetry"
)

const (
	openRouterURL = "https://openrouter.ai/api/v1/chat/completions"
	maxAttempts   = 3
	// A transcript longer than this many prompt characters (~25k tokens) is split into chunks: a huge
	// request is the usual reason a provider times out and OpenRouter answers with an empty body.
	maxChunkChars = 80000
)

// Client asks an OpenRouter model to pick clips from annotated transcript sentences.
type Client struct {
	Key     string
	Model   string
	HTTP    *http.Client
	URL     string        // default: OpenRouter chat completions
	Backoff time.Duration // pause between retries (attempt * Backoff)
	// Effort is the OpenRouter reasoning effort (low|medium|high|xhigh|max). Reasoning models default
	// to a high effort and can think for minutes; picking clips needs little. Empty = do not send.
	Effort string
	// ChunkChars is the max prompt characters per request (default maxChunkChars).
	ChunkChars int
}

func New(key, model string) *Client {
	return &Client{Key: key, Model: model, HTTP: &http.Client{Timeout: 180 * time.Second}, URL: openRouterURL, Backoff: 3 * time.Second, ChunkChars: maxChunkChars}
}

// promptTemplate: the model chooses clip length itself (within hard limits) and picks by sentence
// ids, never by timestamps, so a clip always starts and ends on a sentence boundary.
const promptTemplate = `You are an editor cutting short vertical clips (YouTube Shorts / TikTok) out of a long video.
Below is its transcript split into numbered sentences, one per line:
[id] start_sec +duration_sec {hints} text

Hints appear only when they apply (they are measured against this video's own baseline):
- energy:high = the voice is unusually loud/emotional here compared with the whole video (an emotional peak)
- energy:spike = the voice suddenly got much louder than the seconds before (the speaker raised their voice)
- laugh = laughter or applause follows this sentence (it was probably a joke or a strong moment)
- shout = shouting / yelling / screaming
- gasp = an audible gasp of surprise follows this sentence
- music = music or singing is playing under the speech (speech may be hard to hear; do not start or end a clip in the middle of it unless the music is the point)
- fast = unusually fast speech
- pause_before = a deliberate pause precedes this sentence (a dramatic beat)

Pick the best standalone clips. Rules:
- A clip is a run of consecutive sentences from id "from" to id "to" (inclusive). Use ONLY ids that exist in the transcript. Never invent timestamps.
- Decide each clip's length yourself so that it feels complete: the setup AND the payoff of a joke, a whole thought, story or argument. It must begin where a thought begins and end where it is finished - never cut a thought in half. Typical length is 20-90 seconds; hard limits: at least %d and at most %d seconds in total.
- Hints are extra evidence, not rules. Laughter after a sentence means it was probably a joke, so include the setup before it. An energy peak means an emotional moment. Prefer moments that make sense without the rest of the video.
- Clips must not overlap. Return at most %d clips; fewer is fine if the video has few good moments. Order them by quality, best first.
- "title": a short catchy title in the language of the transcript. "hashtags": 3-5 relevant words without the # sign.

Answer ONLY with JSON: {"highlights":[{"from":number,"to":number,"title":string,"hashtags":[string]}]}

TRANSCRIPT:
%s`

// BuildPrompt renders the prompt (exported for tests).
func BuildPrompt(sents []domain.Sentence, lim domain.ClipLimits) string {
	var tr strings.Builder
	for _, s := range sents {
		hints := ""
		if len(s.Tags) > 0 {
			hints = " {" + strings.Join(s.Tags, " ") + "}"
		}
		fmt.Fprintf(&tr, "[%d] %.1fs +%.1fs%s %s\n", s.ID, s.Start, s.End-s.Start, hints, s.Text)
	}
	return fmt.Sprintf(promptTemplate, int(lim.MinSec), int(lim.MaxSec), lim.MaxClips, tr.String())
}

type rawPick struct {
	From     *int     `json:"from"`
	To       *int     `json:"to"`
	Title    string   `json:"title"`
	Hashtags []string `json:"hashtags"`
}

// ParsePicks extracts picks from the model's answer (tolerates ```json fences).
func ParsePicks(content string) ([]domain.Pick, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var res struct {
		Highlights []rawPick `json:"highlights"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &res); err != nil {
		return nil, fmt.Errorf("bad highlights json: %w", err)
	}
	out := make([]domain.Pick, 0, len(res.Highlights))
	for _, h := range res.Highlights {
		if h.From == nil || h.To == nil {
			continue
		}
		tags := make([]string, 0, len(h.Hashtags))
		for _, t := range h.Hashtags {
			if t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")); t != "" {
				tags = append(tags, t)
			}
		}
		out = append(out, domain.Pick{From: *h.From, To: *h.To, Title: strings.TrimSpace(h.Title), Hashtags: tags})
	}
	return out, nil
}

var spaces = regexp.MustCompile(`\s+`)

// snippet makes a response body safe and short for an error message / log line.
func snippet(b []byte) string {
	t := strings.TrimSpace(spaces.ReplaceAllString(string(b), " "))
	if len(t) > 400 {
		t = t[:400] + "..."
	}
	if t == "" {
		return "<empty>"
	}
	return t
}

type apiError struct {
	Message string `json:"message"`
	Code    any    `json:"code"`
}

func (e *apiError) transient() bool {
	switch v := e.Code.(type) {
	case float64:
		if v == 408 || v == 429 || v >= 500 {
			return true
		}
	}
	m := strings.ToLower(e.Message)
	for _, k := range []string{"timed out", "timeout", "overloaded", "provider returned error", "rate limit", "temporarily", "try again"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// once performs one request. transient=true means a retry may help.
func (c *Client) once(ctx context.Context, body []byte) (content string, transient bool, err error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", ctx.Err() == nil, err // network error / client timeout: retry unless we are shutting down
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500,
			fmt.Errorf("openrouter HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	// OpenRouter keeps a slow request alive with blank lines; a body that is only whitespace means the
	// provider never produced a result (timeout / dropped upstream).
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", true, fmt.Errorf("openrouter returned an empty body (HTTP 200, %d bytes of whitespace): the provider gave no result%s", len(raw), ifErr(rerr))
	}
	var out struct {
		Error   *apiError `json:"error"`
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &out); err != nil {
		return "", true, fmt.Errorf("openrouter returned invalid JSON (%v): %s", err, snippet(raw))
	}
	if out.Error != nil {
		return "", out.Error.transient(), fmt.Errorf("openrouter error %v: %s", out.Error.Code, out.Error.Message)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", true, fmt.Errorf("openrouter returned no completion: %s", snippet(raw))
	}
	return out.Choices[0].Message.Content, false, nil
}

func ifErr(err error) string {
	if err == nil {
		return ""
	}
	return " (read: " + err.Error() + ")"
}

// complete sends the prompt, retrying transient failures with a growing pause.
func (c *Client) complete(ctx context.Context, prompt string) (string, error) {
	req := map[string]any{
		"model":           c.Model,
		"messages":        []map[string]string{{"role": "user", "content": prompt}},
		"response_format": map[string]string{"type": "json_object"},
		"stream":          false,
	}
	if c.Effort != "" {
		req["reasoning"] = map[string]string{"effort": c.Effort}
	}
	body, _ := json.Marshal(req)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		start := time.Now()
		content, transient, err := c.once(ctx, body)
		if err == nil {
			return content, nil
		}
		lastErr = err
		slog.WarnContext(ctx, "llm request failed", "model", c.Model, "attempt", attempt, "of", maxAttempts,
			"prompt_chars", len(prompt), "took", time.Since(start).Round(time.Millisecond), "retry", transient && attempt < maxAttempts, "err", err)
		if !transient || attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(attempt) * c.Backoff):
		}
	}
	return "", fmt.Errorf("llm failed after %d attempts: %w", maxAttempts, lastErr)
}

// splitSentences cuts the transcript into consecutive chunks of at most maxChars prompt characters.
func splitSentences(sents []domain.Sentence, maxChars int) [][]domain.Sentence {
	var chunks [][]domain.Sentence
	var cur []domain.Sentence
	size := 0
	for _, s := range sents {
		l := len(s.Text) + 40 + 14*len(s.Tags)
		if len(cur) > 0 && size+l > maxChars {
			chunks = append(chunks, cur)
			cur, size = nil, 0
		}
		cur = append(cur, s)
		size += l
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// mergeRoundRobin interleaves per-chunk picks by rank so that every chunk's best picks come first
// (the caller keeps the first MaxClips valid ones).
func mergeRoundRobin(lists [][]domain.Pick) []domain.Pick {
	var out []domain.Pick
	for i := 0; ; i++ {
		added := false
		for _, l := range lists {
			if i < len(l) {
				out = append(out, l[i])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}

func (c *Client) Find(ctx context.Context, sents []domain.Sentence, lim domain.ClipLimits) (picks []domain.Pick, err error) {
	ctx, span := telemetry.Start(ctx, "llm find highlights", attribute.String("llm.model", c.Model), attribute.Int("sentences", len(sents)))
	defer func() { telemetry.End(span, err) }()
	if c.Key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	start := time.Now()

	chunkChars := c.ChunkChars
	if chunkChars <= 0 {
		chunkChars = maxChunkChars
	}
	chunks := splitSentences(sents, chunkChars)
	span.SetAttributes(attribute.Int("chunks", len(chunks)))
	chunkLim := lim
	if len(chunks) > 1 {
		chunkLim.MaxClips = max(2, (lim.MaxClips+len(chunks)-1)/len(chunks)+1)
	}
	lists := make([][]domain.Pick, 0, len(chunks))
	for i, ch := range chunks {
		prompt := BuildPrompt(ch, chunkLim)
		slog.InfoContext(ctx, "llm request", "model", c.Model, "reasoning_effort", c.Effort, "timeout", c.HTTP.Timeout, "chunk", i+1, "of", len(chunks), "sentences", len(ch), "prompt_chars", len(prompt))
		content, cerr := c.complete(ctx, prompt)
		if cerr != nil {
			return nil, fmt.Errorf("chunk %d/%d: %w", i+1, len(chunks), cerr)
		}
		p, perr := ParsePicks(content)
		if perr != nil {
			return nil, fmt.Errorf("chunk %d/%d: %w (answer: %s)", i+1, len(chunks), perr, snippet([]byte(content)))
		}
		lists = append(lists, p)
	}
	picks = mergeRoundRobin(lists)
	span.SetAttributes(attribute.Int("picks", len(picks)))
	slog.InfoContext(ctx, "llm answered", "model", c.Model, "chunks", len(chunks), "picks", len(picks), "took", time.Since(start).Round(time.Millisecond))
	return picks, nil
}
