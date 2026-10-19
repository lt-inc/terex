package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestParseTranslationsByIndex(t *testing.T) {
	got, err := ParseTranslations("```json\n{\"words\":[{\"i\":1,\"ru\":\" мир \",\"en\":\"world\"},{\"i\":9,\"ru\":\"x\",\"en\":\"x\"},{\"i\":0,\"ru\":\"привет\",\"en\":\"hello\"}]}\n```", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].RU != "привет" || got[1].RU != "мир" || got[1].EN != "world" || got[2].RU != "" {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestParseTranslationsRejectsEmptyAnswer(t *testing.T) {
	if _, err := ParseTranslations(`{"words":[{"i":7,"ru":"a","en":"b"}]}`, 2); !errors.Is(err, errNoTranslations) {
		t.Fatalf("got %v", err)
	}
	if _, err := ParseTranslations("not json", 2); err == nil {
		t.Fatal("bad json must fail")
	}
}

func TestBuildTranslatePromptNumbersWords(t *testing.T) {
	p := BuildTranslatePrompt([]string{"hi", "there"})
	if !strings.Contains(p, "0\thi\n1\tthere\n") {
		t.Fatalf("words must be numbered from zero:\n%s", p)
	}
}
