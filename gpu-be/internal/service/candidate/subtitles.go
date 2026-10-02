package candidate

import (
	"fmt"
	"strings"

	"terex/gpu-be/internal/domain"
)

func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := int(sec) % 3600 / 60
	s := int(sec) % 60
	ms := int((sec - float64(int(sec))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func assTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := int(sec) % 3600 / 60
	s := int(sec) % 60
	cs := int((sec - float64(int(sec))) * 100)
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

// SRT renders segments as an SRT file.
func SRT(segs []domain.Segment) string {
	var b strings.Builder
	for i, s := range segs {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(s.Start), srtTime(s.End), strings.TrimSpace(s.Text))
	}
	return b.String()
}

const assHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: 1080
PlayResY: 1920
WrapStyle: 0
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Liberation Sans,85,&H0000FFFF,&H00FFFFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,4,2,2,60,60,150,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

// ASS renders per-word karaoke subtitles, wordsPerLine words per line.
func ASS(segs []domain.Segment, wordsPerLine int) string {
	var words []domain.Word
	for _, s := range segs {
		for _, w := range s.Words {
			if strings.TrimSpace(w.Word) != "" {
				words = append(words, domain.Word{Word: strings.TrimSpace(w.Word), Start: w.Start, End: w.End})
			}
		}
	}
	var b strings.Builder
	b.WriteString(assHeader)
	for i := 0; i < len(words); i += wordsPerLine {
		end := min(i+wordsPerLine, len(words))
		chunk := words[i:end]
		parts := make([]string, 0, len(chunk))
		for _, w := range chunk {
			cs := max(1, int((w.End-w.Start)*100))
			parts = append(parts, fmt.Sprintf(`{\kf%d}%s`, cs, w.Word))
		}
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n",
			assTime(chunk[0].Start), assTime(chunk[len(chunk)-1].End), strings.Join(parts, " "))
	}
	return b.String()
}

// GroupSegments merges consecutive segments until each group spans >= minDuration seconds;
// a short tail is merged into the previous group. IDs are sequential from 0.
func GroupSegments(segs []domain.Segment, minDuration float64) []domain.Interval {
	var out []domain.Interval
	var cur *domain.Interval
	for _, s := range segs {
		if cur == nil {
			cur = &domain.Interval{Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text)}
		} else {
			cur.End = s.End
			cur.Text += " " + strings.TrimSpace(s.Text)
		}
		if cur.End-cur.Start >= minDuration {
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil {
		if len(out) > 0 {
			last := &out[len(out)-1]
			last.End = cur.End
			last.Text += " " + cur.Text
		} else {
			out = append(out, *cur)
		}
	}
	for i := range out {
		out[i].ID = i
	}
	return out
}
