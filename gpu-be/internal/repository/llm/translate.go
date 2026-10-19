package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/proto/telemetry"
)

// translateChunkWords bounds one request: a Short has a few hundred words, a long transcript is split.
const translateChunkWords = 250

const translatePrompt = `You translate the speech of a short video into word-by-word bilingual subtitles.
Below are the spoken words, numbered, in the order they are spoken. They form continuous speech, so translate each word in its context.

For EVERY index give "ru" (Russian) and "en" (English): what that word means here, as a single word or a very short phrase, so that reading all "ru" values in order gives a faithful Russian rendering of the speech and all "en" values a faithful English one.
- If the speech is already Russian, "ru" is the word itself; if it is English, "en" is the word itself.
- When several source words form one target word or phrase, put it on the first of them and use "" for the rest.
- Never skip an index, never merge indexes, never add commentary.

Answer ONLY with JSON: {"words":[{"i":number,"ru":string,"en":string}]}

WORDS:
%s`

var errNoTranslations = errors.New("llm returned no usable word translations")

// BuildTranslatePrompt renders the prompt for words numbered from zero (exported for tests).
func BuildTranslatePrompt(words []string) string {
	var b strings.Builder
	for i, w := range words {
		fmt.Fprintf(&b, "%d\t%s\n", i, w)
	}
	return fmt.Sprintf(translatePrompt, b.String())
}

// ParseTranslations maps the model's answer onto n words by index; words the model skipped stay empty.
func ParseTranslations(content string, n int) ([]domain.WordTranslation, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var res struct {
		Words []struct {
			I  *int   `json:"i"`
			RU string `json:"ru"`
			EN string `json:"en"`
		} `json:"words"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &res); err != nil {
		return nil, fmt.Errorf("bad translation json: %w", err)
	}
	out := make([]domain.WordTranslation, n)
	used := 0
	for _, w := range res.Words {
		if w.I == nil || *w.I < 0 || *w.I >= n {
			continue
		}
		out[*w.I] = domain.WordTranslation{RU: strings.TrimSpace(w.RU), EN: strings.TrimSpace(w.EN)}
		used++
	}
	if used == 0 && n > 0 {
		return nil, errNoTranslations
	}
	return out, nil
}

// TranslateWords returns one RU+EN pair per spoken word, in order.
func (c *Client) TranslateWords(ctx context.Context, words []string) (out []domain.WordTranslation, err error) {
	ctx, span := telemetry.Start(ctx, "llm translate words", attribute.String("llm.model", c.Model), attribute.Int("words", len(words)))
	defer func() { telemetry.End(span, err) }()
	if len(words) == 0 {
		return nil, nil
	}
	if c.Key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	start := time.Now()
	out = make([]domain.WordTranslation, 0, len(words))
	for from := 0; from < len(words); from += translateChunkWords {
		chunk := words[from:min(from+translateChunkWords, len(words))]
		prompt := BuildTranslatePrompt(chunk)
		var part []domain.WordTranslation
		for attempt := 1; ; attempt++ {
			var content string
			if content, err = c.complete(ctx, prompt); err != nil {
				return nil, fmt.Errorf("words %d-%d: %w", from, from+len(chunk), err)
			}
			if part, err = ParseTranslations(content, len(chunk)); err == nil {
				break
			}
			if attempt == maxAttempts {
				return nil, fmt.Errorf("words %d-%d: %w (answer: %s)", from, from+len(chunk), err, snippet([]byte(content)))
			}
			slog.WarnContext(ctx, "llm translation unusable; retrying", "attempt", attempt, "of", maxAttempts, "err", err)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * c.Backoff):
			}
		}
		out = append(out, part...)
	}
	slog.InfoContext(ctx, "llm translated words", "model", c.Model, "words", len(words), "took", time.Since(start).Round(time.Millisecond))
	return out, nil
}
