package candidate

import (
	"fmt"
	"testing"

	"terex/gpu-be/internal/domain"
)

func w(word string, s, e float64) domain.Word { return domain.Word{Word: word, Start: s, End: e} }

func TestBuildSentencesSplitsOnPunctuationAndPause(t *testing.T) {
	segs := []domain.Segment{{Words: []domain.Word{
		w(" Hello", 0, 0.4), w(" world.", 0.5, 1.0), // punctuation
		w(" next", 1.1, 1.4), w(" bit", 1.5, 1.8), // no punctuation, then a long pause
		w(" after", 3.5, 3.9), w(" pause?", 4.0, 4.5),
	}}}
	got := BuildSentences(segs)
	if len(got) != 3 {
		t.Fatalf("want 3 sentences, got %+v", got)
	}
	if got[0].Text != "Hello world." || got[0].Start != 0 || got[0].End != 1.0 {
		t.Fatalf("bad first sentence %+v", got[0])
	}
	if got[1].Text != "next bit" || got[2].Start != 3.5 || got[2].ID != 2 {
		t.Fatalf("bad split %+v", got)
	}
}

func TestBuildSentencesForceSplitsRunOn(t *testing.T) {
	var words []domain.Word
	for i := 0; i < 80; i++ { // 80 words * 0.5s = 40s without punctuation or pauses
		words = append(words, w(fmt.Sprintf(" w%d", i), float64(i)*0.5, float64(i)*0.5+0.45))
	}
	got := BuildSentences([]domain.Segment{{Words: words}})
	if len(got) < 2 {
		t.Fatalf("run-on must be split, got %d", len(got))
	}
	for _, s := range got {
		if s.End-s.Start > maxSentenceSecs+0.5 {
			t.Fatalf("sentence too long: %+v", s)
		}
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(v, 90); got < 9.09 || got > 9.11 {
		t.Fatalf("p90 = %v", got)
	}
}

func mkSents(n int) []domain.Sentence {
	s := make([]domain.Sentence, n)
	for i := range s {
		s[i] = domain.Sentence{ID: i, Start: float64(i) * 5, End: float64(i)*5 + 4, Words: 12, Text: fmt.Sprintf("s%d", i)}
	}
	return s
}

func TestAnnotateTagsOnlyOutliers(t *testing.T) {
	sents := mkSents(20)
	// energy: windows of 0.4s; make sentence 7 loud (-5 dB) and the rest quiet (-40 dB)
	e := domain.EnergySeries{Step: 0.4}
	for i := 0; i < int(100/0.4); i++ {
		db := -40.0
		if t0 := float64(i) * 0.4; t0 >= sents[7].Start && t0 < sents[7].End {
			db = -5
		}
		e.DB = append(e.DB, db)
	}
	// laughter right after sentence 12
	laugh := []domain.AudioEvent{{Start: sents[12].End, End: sents[12].End + 1, Laugh: 0.9}, {Start: 0, End: 1, Laugh: 0.05}}
	Annotate(sents, e, laugh)

	if !hasTag(sents[7], TagEnergyHigh) {
		t.Fatalf("loud sentence not tagged: %+v", sents[7])
	}
	if !hasTag(sents[12], TagLaugh) || hasTag(sents[3], TagLaugh) {
		t.Fatalf("laugh tags wrong: s12=%v s3=%v", sents[12].Tags, sents[3].Tags)
	}
	tagged := 0
	for _, s := range sents {
		if hasTag(s, TagEnergyHigh) {
			tagged++
		}
	}
	if tagged > 3 {
		t.Fatalf("energy:high must be rare, got %d of %d", tagged, len(sents))
	}
}

func TestAnnotateNoLaughBelowFloor(t *testing.T) {
	sents := mkSents(20)
	Annotate(sents, domain.EnergySeries{}, []domain.AudioEvent{{Start: sents[5].End, End: sents[5].End + 1, Laugh: 0.1}})
	for _, s := range sents {
		if hasTag(s, TagLaugh) {
			t.Fatalf("weak laughter must not be tagged: %+v", s)
		}
	}
}

func TestAnnotateTooFewSentences(t *testing.T) {
	sents := mkSents(3)
	Annotate(sents, domain.EnergySeries{Step: 0.4, DB: []float64{-5, -40, -40, -40}}, nil)
	for _, s := range sents {
		if len(s.Tags) > 0 {
			t.Fatal("no tags expected for a tiny video")
		}
	}
}

func TestFilterTags(t *testing.T) {
	s := []domain.Sentence{{Tags: []string{TagEnergyHigh, TagFast, TagLaugh}}}
	got := FilterTags(s, []string{TagEnergyHigh, TagLaugh})
	if len(got[0].Tags) != 2 || len(s[0].Tags) != 3 {
		t.Fatalf("filter wrong or mutated input: %+v %+v", got, s)
	}
}

var lim = domain.ClipLimits{MinSec: 15, MaxSec: 60, MaxClips: 5}

func TestResolvePicksBoundsAndPadding(t *testing.T) {
	sents := mkSents(20) // sentence i: [5i, 5i+4], 1s gap between
	out, dropped := ResolvePicks(sents, []domain.Pick{{From: 4, To: 7, Title: "t", Hashtags: []string{"a"}}}, lim)
	if dropped != 0 || len(out) != 1 {
		t.Fatalf("out=%+v dropped=%d", out, dropped)
	}
	// start: 20 - min(0.2, 1/2)=19.8 ; end: 39 + min(0.3, 1/2)=39.3 — padding stays inside the 1s gaps
	if out[0].Start != 19.8 || out[0].End != 39.3 || out[0].Title != "t" {
		t.Fatalf("unexpected interval %+v", out[0])
	}
}

func TestResolvePicksNeverCrossesNeighbours(t *testing.T) {
	sents := []domain.Sentence{
		{ID: 0, Start: 0, End: 10}, {ID: 1, Start: 10.05, End: 30}, {ID: 2, Start: 30.04, End: 50},
	}
	out, _ := ResolvePicks(sents, []domain.Pick{{From: 1, To: 1}}, domain.ClipLimits{MinSec: 10, MaxSec: 60, MaxClips: 3})
	if len(out) != 1 || out[0].Start < 10 || out[0].End > 30.04 {
		t.Fatalf("pad crossed a neighbour: %+v", out)
	}
}

func TestResolvePicksLaughKeepsReaction(t *testing.T) {
	sents := mkSents(20)
	sents[7].Tags = []string{TagLaugh}
	sents[8].Start, sents[8].End = 43.0, 47.0 // 4s gap after sentence 7 (ends at 39)
	out, _ := ResolvePicks(sents, []domain.Pick{{From: 4, To: 7}}, lim)
	if out[0].End != 39+1.5 {
		t.Fatalf("laugh reaction should be kept (1.5s), got end=%v", out[0].End)
	}
}

func TestResolvePicksDropsInvalidOverlapAndCaps(t *testing.T) {
	sents := mkSents(40)
	picks := []domain.Pick{
		{From: 10, To: 14}, // ok: 24s
		{From: 12, To: 16}, // overlaps the first -> dropped
		{From: 30, To: 99}, // out of range -> dropped
		{From: 20, To: 20}, // 4s, extended to >= 15s
		{From: 25, To: 24}, // reversed -> swapped, 4s..9s extended
		{From: 0, To: 39},  // 199s -> trimmed to <= 60s but overlaps -> dropped
	}
	out, dropped := ResolvePicks(sents, picks, domain.ClipLimits{MinSec: 15, MaxSec: 60, MaxClips: 3})
	if len(out) != 3 {
		t.Fatalf("want 3 clips (cap), got %d: %+v", len(out), out)
	}
	for i, iv := range out {
		d := iv.End - iv.Start
		if d < 15 || d > 62 {
			t.Fatalf("clip %d length %v outside limits", i, d)
		}
		if i > 0 && iv.Start < out[i-1].End {
			t.Fatalf("clips overlap: %+v", out)
		}
		if iv.ID != i {
			t.Fatalf("ids must be sequential: %+v", out)
		}
	}
	if dropped < 3 {
		t.Fatalf("dropped = %d", dropped)
	}
}

func TestResolvePicksTrimsTooLong(t *testing.T) {
	sents := mkSents(40)
	out, _ := ResolvePicks(sents, []domain.Pick{{From: 0, To: 30}}, lim)
	if len(out) != 1 || out[0].End-out[0].Start > 61 {
		t.Fatalf("too-long clip must be trimmed: %+v", out)
	}
}

// flatEnergy builds a series where every sentence is at base dB, with overrides per sentence index.
func flatEnergy(sents []domain.Sentence, base float64, over map[int]float64) domain.EnergySeries {
	e := domain.EnergySeries{Step: 0.4}
	end := sents[len(sents)-1].End + 1
	for i := 0; float64(i)*0.4 < end; i++ {
		t0 := float64(i) * 0.4
		db := base
		for idx, v := range over {
			if t0 >= sents[idx].Start && t0 < sents[idx].End {
				db = v
			}
		}
		e.DB = append(e.DB, db)
	}
	return e
}

func TestEnergySpikeIsRelativeToNeighbours(t *testing.T) {
	// loud video: everything at -10 dB, sentence 12 jumps to -2 dB (+8 dB vs the neighbours)
	sents := mkSents(20)
	Annotate(sents, flatEnergy(sents, -10, map[int]float64{12: -2}), nil)
	if !hasTag(sents[12], TagEnergySpike) {
		t.Fatalf("a jump above the local baseline must be tagged: %v", sents[12].Tags)
	}
	for i, s := range sents {
		if i != 12 && hasTag(s, TagEnergySpike) {
			t.Fatalf("only the jump may be tagged, got sentence %d", i)
		}
	}
}

func TestEnergySpikeNeedsBaselineAndSize(t *testing.T) {
	sents := mkSents(20)
	// +4 dB is below the 6 dB threshold
	Annotate(sents, flatEnergy(sents, -20, map[int]float64{10: -16}), nil)
	for _, s := range sents {
		if hasTag(s, TagEnergySpike) {
			t.Fatalf("a +4 dB change is not a spike: %+v", s)
		}
	}
	// first sentences have no baseline -> never a spike even when loud
	sents = mkSents(20)
	Annotate(sents, flatEnergy(sents, -40, map[int]float64{0: -5, 1: -5}), nil)
	if hasTag(sents[0], TagEnergySpike) || hasTag(sents[1], TagEnergySpike) {
		t.Fatal("no baseline yet, no spike")
	}
}

func TestEnergySpikeIgnoresOldBaseline(t *testing.T) {
	// sentences are 5 s apart: the baseline only looks 45 s back, so a gradual rise is not a spike
	sents := mkSents(40)
	over := map[int]float64{}
	for i := 20; i < 40; i++ {
		over[i] = -20 + float64(i-20)*0.5 // slow ramp: +0.5 dB per sentence
	}
	Annotate(sents, flatEnergy(sents, -20, over), nil)
	for i, s := range sents {
		if hasTag(s, TagEnergySpike) {
			t.Fatalf("gradual ramp tagged at %d", i)
		}
	}
}

func TestAudioEventTags(t *testing.T) {
	sents := mkSents(20)
	ev := []domain.AudioEvent{
		{Start: sents[6].Start, End: sents[6].End, Shout: 0.8},            // shouting during s6
		{Start: sents[9].End, End: sents[9].End + 1, Gasp: 0.7},           // gasp right after s9
		{Start: sents[3].Start, End: sents[3].End, Shout: 0.1, Gasp: 0.1}, // noise
		{Start: sents[14].Start, End: sents[14].End, Music: 0.9},          // music under s14 only
		{Start: sents[15].Start, End: sents[15].End, Music: 0.9},
	}
	Annotate(sents, domain.EnergySeries{}, ev)
	if !hasTag(sents[6], TagShout) || hasTag(sents[3], TagShout) {
		t.Fatalf("shout: s6=%v s3=%v", sents[6].Tags, sents[3].Tags)
	}
	if !hasTag(sents[9], TagGasp) || hasTag(sents[3], TagGasp) {
		t.Fatalf("gasp: s9=%v s3=%v", sents[9].Tags, sents[3].Tags)
	}
	if !hasTag(sents[14], TagMusic) || !hasTag(sents[15], TagMusic) || hasTag(sents[2], TagMusic) {
		t.Fatalf("music: s14=%v s2=%v", sents[14].Tags, sents[2].Tags)
	}
}

func TestMusicEverywhereIsNotAHint(t *testing.T) {
	sents := mkSents(20)
	var ev []domain.AudioEvent
	for _, s := range sents { // background music under the whole video
		ev = append(ev, domain.AudioEvent{Start: s.Start, End: s.End, Music: 0.95})
	}
	Annotate(sents, domain.EnergySeries{}, ev)
	for _, s := range sents {
		if hasTag(s, TagMusic) {
			t.Fatalf("baseline music must not be tagged: %+v", s)
		}
	}
}
