package candidate

import (
	"strings"

	"terex/gpu-be/internal/domain"
)

const (
	pauseSplit      = 1.0  // a silence this long between words ends a sentence
	maxSentenceSecs = 25.0 // run-on speech without punctuation is force-split
)

func endsSentence(w string) bool {
	w = strings.TrimRight(strings.TrimSpace(w), `"'”’»)]`)
	return strings.HasSuffix(w, ".") || strings.HasSuffix(w, "!") || strings.HasSuffix(w, "?") || strings.HasSuffix(w, "…")
}

// BuildSentences groups whisper words into sentences. Sentence bounds are the first/last word
// timestamps, so cutting at them never splits a word. A segment without word timestamps becomes
// one sentence.
func BuildSentences(segs []domain.Segment) []domain.Sentence {
	var words []domain.Word
	for _, s := range segs {
		if len(s.Words) == 0 {
			if t := strings.TrimSpace(s.Text); t != "" {
				words = append(words, domain.Word{Word: t + " ", Start: s.Start, End: s.End})
			}
			continue
		}
		for _, w := range s.Words {
			if strings.TrimSpace(w.Word) != "" {
				words = append(words, w)
			}
		}
	}

	var out []domain.Sentence
	var cur []domain.Word
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, len(cur))
		for i, w := range cur {
			parts[i] = strings.TrimSpace(w.Word)
		}
		out = append(out, domain.Sentence{
			ID: len(out), Start: cur[0].Start, End: cur[len(cur)-1].End,
			Text: strings.Join(parts, " "), Words: len(cur),
		})
		cur = nil
	}
	for i, w := range words {
		cur = append(cur, w)
		last := i == len(words)-1
		gap := 0.0
		if !last {
			gap = words[i+1].Start - w.End
		}
		if last || endsSentence(w.Word) || gap >= pauseSplit || w.End-cur[0].Start >= maxSentenceSecs {
			flush()
		}
	}
	return out
}
