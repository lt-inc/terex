package candidate

import (
	"sort"
	"strings"

	"terex/gpu-be/internal/domain"
)

const (
	leadPad       = 0.20 // seconds kept before the first word (never into the previous sentence)
	trailPad      = 0.30 // seconds kept after the last word
	laughTrailPad = 1.50 // keep the laughter/applause reaction after a punchline
)

// ResolvePicks turns the LLM's sentence-id picks into safe intervals:
//   - ids are validated, swapped if reversed, out-of-range picks are dropped;
//   - length is forced into [MinSec, MaxSec] by trimming/extending whole sentences;
//   - overlapping picks are dropped (the LLM's order = quality order wins);
//   - timestamps come from word bounds plus a small pad that never reaches a neighbour sentence,
//     so a cut can't land inside a word.
//
// It returns the intervals ordered by time and how many picks were dropped.
func ResolvePicks(sents []domain.Sentence, picks []domain.Pick, lim domain.ClipLimits) ([]domain.Interval, int) {
	type span struct {
		from, to int
		pick     domain.Pick
	}
	dur := func(a, b int) float64 { return sents[b].End - sents[a].Start }
	overlaps := func(acc []span, from, to int) bool {
		for _, s := range acc {
			if from <= s.to && to >= s.from {
				return true
			}
		}
		return false
	}

	var acc []span
	dropped := 0
	for _, p := range picks {
		if lim.MaxClips > 0 && len(acc) >= lim.MaxClips {
			dropped++
			continue
		}
		from, to := p.From, p.To
		if from > to {
			from, to = to, from
		}
		if from < 0 || to >= len(sents) {
			dropped++
			continue
		}
		for lim.MaxSec > 0 && dur(from, to) > lim.MaxSec && to > from {
			to-- // too long: drop trailing sentences
		}
		if lim.MaxSec > 0 && dur(from, to) > lim.MaxSec {
			dropped++
			continue
		}
		for dur(from, to) < lim.MinSec { // too short: grow to complete the thought
			if to+1 < len(sents) && (lim.MaxSec <= 0 || dur(from, to+1) <= lim.MaxSec) && !overlaps(acc, from, to+1) {
				to++
			} else if from > 0 && (lim.MaxSec <= 0 || dur(from-1, to) <= lim.MaxSec) && !overlaps(acc, from-1, to) {
				from--
			} else {
				break
			}
		}
		if dur(from, to) < lim.MinSec || overlaps(acc, from, to) {
			dropped++
			continue
		}
		acc = append(acc, span{from, to, p})
	}
	sort.Slice(acc, func(i, j int) bool { return acc[i].from < acc[j].from })

	out := make([]domain.Interval, 0, len(acc))
	for i, a := range acc {
		first, last := sents[a.from], sents[a.to]

		prevEnd := 0.0
		if a.from > 0 {
			prevEnd = sents[a.from-1].End
		}
		lead := minF(leadPad, (first.Start-prevEnd)/2)
		start := maxF(0, first.Start-maxF(lead, 0))

		var gapAfter float64 = laughTrailPad // end of video: nothing to collide with
		if a.to+1 < len(sents) {
			gapAfter = sents[a.to+1].Start - last.End
		}
		var trail float64
		if hasTag(last, TagLaugh) {
			trail = minF(laughTrailPad, maxF(0, gapAfter-0.1))
		} else {
			trail = minF(trailPad, gapAfter/2)
		}
		end := last.End + maxF(trail, 0)

		texts := make([]string, 0, a.to-a.from+1)
		for k := a.from; k <= a.to; k++ {
			texts = append(texts, sents[k].Text)
		}
		out = append(out, domain.Interval{
			ID: i, Start: start, End: end, Text: strings.Join(texts, " "),
			Title: a.pick.Title, Tags: a.pick.Hashtags,
		})
	}
	return out, dropped
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
