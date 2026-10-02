package candidate

import (
	"math"
	"sort"

	"terex/gpu-be/internal/domain"
)

// Tag names shown to the LLM. A tag is only written when it is true (empty values are omitted).
const (
	TagEnergyHigh  = "energy:high"
	TagEnergySpike = "energy:spike"
	TagLaugh       = "laugh"
	TagShout       = "shout"
	TagGasp        = "gasp"
	TagMusic       = "music"
	TagFast        = "fast"
	TagPauseBefore = "pause_before"
)

// Thresholds are percentiles of the video itself, never absolute values, so a quiet podcast and a
// loud stream are judged against their own baseline.
const (
	pBase   = 60.0 // a value must also be strictly above the video's p60 (kills ties on flat distributions)
	pEnergy = 90.0
	pLaugh  = 90.0
	pFast   = 90.0
	pPause  = 90.0

	laughFloor     = 0.30 // laughter probability below this is noise even if it is the video's p90
	pauseFloorSecs = 0.7  // a "dramatic" pause is at least this long
	minForPercent  = 8    // fewer sentences than this: percentiles are meaningless, no tags
	laughBefore    = 0.3  // laughter window may start this long before the sentence end ...
	laughAfter     = 2.0  // ... and up to this long after it (the reaction follows the punchline)

	// energy:spike - the sentence is much louder than its recent neighbours (works in a loud video too).
	spikeDB          = 6.0  // dB above the median of the preceding sentences (6 dB = 2x amplitude)
	spikeWindowSecs  = 45.0 // "recent" = this many seconds before the sentence
	spikeMinBaseline = 3    // need at least this many earlier sentences for a baseline

	shoutFloor = 0.25 // YAMNet probability floors: below these a window is noise even at the video's p90
	gaspFloor  = 0.25
	gaspAfter  = 1.5 // a gasp reacts to what was just said
	musicFloor = 0.50
	// music under speech is only a hint when it is not the whole video's background
	musicMaxShare = 0.60
)

func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	pos := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

// high reports whether v is an outlier of its distribution: >= p(hi) and strictly above p60.
func high(v float64, vals []float64, hi float64) bool {
	return v >= percentile(vals, hi) && v > percentile(vals, pBase)
}

func sentenceEnergy(s domain.Sentence, e domain.EnergySeries) (float64, bool) {
	if e.Step <= 0 || len(e.DB) == 0 {
		return 0, false
	}
	a := int(s.Start / e.Step)
	b := int(math.Ceil(s.End / e.Step))
	a, b = max(a, 0), min(b, len(e.DB))
	if a >= b {
		return 0, false
	}
	var sum float64
	for _, v := range e.DB[a:b] {
		sum += v
	}
	return sum / float64(b-a), true
}

// Annotate adds hint tags to sentences in place (relative to the video's own distribution).
func Annotate(sents []domain.Sentence, energy domain.EnergySeries, events []domain.AudioEvent) {
	if len(sents) < minForPercent {
		return
	}
	add := func(i int, tag string) { sents[i].Tags = append(sents[i].Tags, tag) }

	// energy: mean loudness of the sentence vs the video's p90
	en := make([]float64, len(sents))
	var known []float64
	for i, s := range sents {
		if v, ok := sentenceEnergy(s, energy); ok {
			en[i] = v
			known = append(known, v)
		} else {
			en[i] = math.NaN()
		}
	}
	for i := range sents {
		if !math.IsNaN(en[i]) && high(en[i], known, pEnergy) {
			add(i, TagEnergyHigh)
		}
	}

	// energy:spike - loudness jump vs the median of the previous ~45 s of sentences
	for i := range sents {
		if math.IsNaN(en[i]) {
			continue
		}
		var base []float64
		for j := 0; j < i; j++ {
			if sents[j].Start >= sents[i].Start-spikeWindowSecs && !math.IsNaN(en[j]) {
				base = append(base, en[j])
			}
		}
		if len(base) >= spikeMinBaseline && en[i]-percentile(base, 50) >= spikeDB {
			add(i, TagEnergySpike)
		}
	}

	annotateEvents(sents, events, add)

	// speech rate (words/sec) vs the video's p90
	rates := make([]float64, len(sents))
	var allRates []float64
	for i, s := range sents {
		if d := s.End - s.Start; d >= 1.0 && s.Words >= 3 {
			rates[i] = float64(s.Words) / d
			allRates = append(allRates, rates[i])
		}
	}
	for i := range sents {
		if rates[i] > 0 && high(rates[i], allRates, pFast) {
			add(i, TagFast)
		}
	}

	// pause before the sentence vs the video's p90 (and at least pauseFloorSecs)
	var pauses []float64
	for i := 1; i < len(sents); i++ {
		pauses = append(pauses, math.Max(0, sents[i].Start-sents[i-1].End))
	}
	for i := 1; i < len(sents); i++ {
		gap := sents[i].Start - sents[i-1].End
		if gap >= pauseFloorSecs && high(gap, pauses, pPause) {
			add(i, TagPauseBefore)
		}
	}
}

// FilterTags returns a copy of sents keeping only the allowed tags (what the LLM gets to see).
func FilterTags(sents []domain.Sentence, allowed []string) []domain.Sentence {
	ok := map[string]bool{}
	for _, t := range allowed {
		ok[t] = true
	}
	out := make([]domain.Sentence, len(sents))
	for i, s := range sents {
		s.Tags = nil
		for _, t := range sents[i].Tags {
			if ok[t] {
				s.Tags = append(s.Tags, t)
			}
		}
		out[i] = s
	}
	return out
}

func hasTag(s domain.Sentence, tag string) bool {
	for _, t := range s.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// annotateEvents adds laugh / shout / gasp / music from YAMNet windows. Thresholds are the video's p90
// of that event's score with an absolute floor, so one stray window in a quiet video is not a "gasp".
func annotateEvents(sents []domain.Sentence, events []domain.AudioEvent, add func(int, string)) {
	if len(events) == 0 {
		return
	}
	overlap := func(lo, hi float64, score func(domain.AudioEvent) float64) (max, mean float64) {
		n := 0
		for _, w := range events {
			if w.End > lo && w.Start < hi {
				v := score(w)
				if v > max {
					max = v
				}
				mean += v
				n++
			}
		}
		if n > 0 {
			mean /= float64(n)
		}
		return max, mean
	}
	p90 := func(score func(domain.AudioEvent) float64) float64 {
		vals := make([]float64, len(events))
		for i, w := range events {
			vals[i] = score(w)
		}
		return percentile(vals, 90)
	}
	laugh := func(w domain.AudioEvent) float64 { return w.Laugh }
	shout := func(w domain.AudioEvent) float64 { return w.Shout }
	gasp := func(w domain.AudioEvent) float64 { return w.Gasp }
	music := func(w domain.AudioEvent) float64 { return w.Music }

	laughTh := math.Max(p90(laugh), laughFloor)
	shoutTh := math.Max(p90(shout), shoutFloor)
	gaspTh := math.Max(p90(gasp), gaspFloor)

	var musicIdx []int
	for i, s := range sents {
		if m, _ := overlap(s.End-laughBefore, s.End+laughAfter, laugh); m >= laughTh {
			add(i, TagLaugh)
		}
		if m, _ := overlap(s.Start, s.End, shout); m >= shoutTh {
			add(i, TagShout)
		}
		if m, _ := overlap(s.End-laughBefore, s.End+gaspAfter, gasp); m >= gaspTh {
			add(i, TagGasp)
		}
		if _, mean := overlap(s.Start, s.End, music); mean >= musicFloor {
			musicIdx = append(musicIdx, i)
		}
	}
	if float64(len(musicIdx)) <= musicMaxShare*float64(len(sents)) {
		for _, i := range musicIdx {
			add(i, TagMusic)
		}
	}
}
