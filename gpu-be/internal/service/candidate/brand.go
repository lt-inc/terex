package candidate

import (
	"context"
	"fmt"
	"strings"

	"terex/gpu-be/internal/domain"
)

// brandAssHeader: yellow bold text, top-centre aligned; MarginV (the %d) is the distance from the top of the
// 1080x1920 frame, i.e. just below the shrunk video.
const brandAssHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: 1080
PlayResY: 1920
WrapStyle: 0
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Liberation Sans,60,&H0000FFFF,&H0000FFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,4,2,8,40,40,%d,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

// brandGap: a pause shorter than this keeps the previous word on screen, so the text does not flicker between words.
const brandGap = 0.4

type brandItem struct {
	start, end float64
	ru, en     string
}

var assEscaper = strings.NewReplacer("{", "｛", "}", "｝", `\`, "＼", "\r", " ", "\n", " ")

// brandItems turns spoken words and their translations into screen states. A side without its own
// translation keeps the previous text; consecutive identical states are merged.
func brandItems(words []domain.Word, tr []domain.WordTranslation) []brandItem {
	var items []brandItem
	var ru, en string
	for i, w := range words {
		var t domain.WordTranslation
		if i < len(tr) {
			t = tr[i]
		}
		nru, nen := strings.TrimSpace(assEscaper.Replace(t.RU)), strings.TrimSpace(assEscaper.Replace(t.EN))
		if nru == "" {
			nru = ru
		}
		if nen == "" {
			nen = en
		}
		end := w.End
		if i+1 < len(words) && words[i+1].Start-end <= brandGap {
			end = words[i+1].Start
		}
		if end <= w.Start {
			end = w.Start + 0.1
		}
		if nru == "" && nen == "" {
			continue
		}
		if n := len(items); n > 0 && items[n-1].ru == nru && items[n-1].en == nen && w.Start-items[n-1].end <= brandGap {
			items[n-1].end = end
		} else {
			items = append(items, brandItem{start: w.Start, end: end, ru: nru, en: nen})
		}
		ru, en = nru, nen
	}
	return items
}

// BrandASS renders the branded subtitles: at any moment one RU word above one EN word, both yellow,
// whichever is being spoken. marginTop is the y of the first line (the bottom edge of the video).
func BrandASS(words []domain.Word, tr []domain.WordTranslation, marginTop int) string {
	var b strings.Builder
	fmt.Fprintf(&b, brandAssHeader, marginTop)
	for _, it := range brandItems(words, tr) {
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\\N%s\n", assTime(it.start), assTime(it.end), it.ru, it.en)
	}
	return b.String()
}

// spokenWords flattens segments into the words that are shown one by one.
func spokenWords(segs []domain.Segment) []domain.Word {
	var words []domain.Word
	for _, s := range segs {
		for _, w := range s.Words {
			if t := strings.TrimSpace(w.Word); t != "" {
				words = append(words, domain.Word{Word: t, Start: w.Start, End: w.End})
			}
		}
	}
	return words
}

// brandSubtitles translates the transcript word by word and renders the ASS file. A video without speech
// gets an empty (event-less) file: music-only Shorts must not fail.
func (s *Service) brandSubtitles(ctx context.Context, segs []domain.Segment, marginTop int) (string, error) {
	words := spokenWords(segs)
	if len(words) == 0 {
		return BrandASS(nil, nil, marginTop), nil
	}
	if s.Translator == nil {
		return "", fmt.Errorf("word translator is not configured")
	}
	texts := make([]string, len(words))
	for i, w := range words {
		texts[i] = w.Word
	}
	tr, err := s.Translator.TranslateWords(ctx, texts)
	if err != nil {
		return "", fmt.Errorf("translate subtitles: %w", err)
	}
	return BrandASS(words, tr, marginTop), nil
}
