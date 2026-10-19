package candidate

import (
	"context"
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
)

func TestBrandASSShowsOneWordPerLanguage(t *testing.T) {
	words := []domain.Word{{Word: "Hello", Start: 0, End: 0.4}, {Word: "world", Start: 0.5, End: 1}}
	tr := []domain.WordTranslation{{RU: "Привет", EN: "Hello"}, {RU: "мир", EN: "world"}}
	ass := BrandASS(words, tr, 1452)
	if !strings.Contains(ass, ",8,40,40,1452,1") {
		t.Fatalf("style must be top-centre with the margin under the video:\n%s", ass)
	}
	if !strings.Contains(ass, "0:00:00.00,0:00:00.50,Default,,0,0,0,,Привет\\NHello") {
		t.Fatalf("first word must stay until the next starts:\n%s", ass)
	}
	if !strings.Contains(ass, "0:00:00.50,0:00:01.00,Default,,0,0,0,,мир\\Nworld") {
		t.Fatalf("second word missing:\n%s", ass)
	}
}

func TestBrandItemsCarryMissingSideAndMerge(t *testing.T) {
	words := []domain.Word{{Word: "a", Start: 0, End: 0.3}, {Word: "b", Start: 0.3, End: 0.6}, {Word: "c", Start: 0.6, End: 0.9}}
	tr := []domain.WordTranslation{{RU: "раз", EN: "one"}, {}, {RU: "{два}", EN: ""}}
	items := brandItems(words, tr)
	if len(items) != 2 {
		t.Fatalf("empty translation must extend the previous word, got %+v", items)
	}
	if items[0].end != 0.6 || items[0].ru != "раз" {
		t.Fatalf("unexpected first item %+v", items[0])
	}
	if items[1].ru != "｛два｝" || items[1].en != "one" {
		t.Fatalf("braces must be escaped and EN carried over: %+v", items[1])
	}
}

type fakeTranslator struct{ got []string }

func (f *fakeTranslator) TranslateWords(_ context.Context, w []string) ([]domain.WordTranslation, error) {
	f.got = w
	out := make([]domain.WordTranslation, len(w))
	for i := range w {
		out[i] = domain.WordTranslation{RU: "р" + w[i], EN: "e" + w[i]}
	}
	return out, nil
}

func TestBrandSubtitlesSilentVideoHasNoEvents(t *testing.T) {
	s := &Service{}
	ass, err := s.brandSubtitles(context.Background(), nil, 1452)
	if err != nil || strings.Contains(ass, "Dialogue:") {
		t.Fatalf("silent video: err=%v ass=%s", err, ass)
	}
}

func TestBrandSubtitlesTranslatesTrimmedWords(t *testing.T) {
	tr := &fakeTranslator{}
	s := &Service{Translator: tr}
	segs := []domain.Segment{{Words: []domain.Word{{Word: " hi", Start: 0, End: 1}, {Word: " ", Start: 1, End: 2}}}}
	ass, err := s.brandSubtitles(context.Background(), segs, 1452)
	if err != nil || len(tr.got) != 1 || tr.got[0] != "hi" || !strings.Contains(ass, "рhi\\Nehi") {
		t.Fatalf("err=%v got=%v ass=%s", err, tr.got, ass)
	}
}
