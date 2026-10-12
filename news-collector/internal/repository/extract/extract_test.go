package extract

import (
	"strings"
	"testing"
)

func TestTextPicksArticleBody(t *testing.T) {
	para := strings.Repeat("Это длинный абзац настоящей статьи с достаточным количеством слов. ", 3)
	page := `<html><body><nav><p>Главная · Новости · Контакты · Реклама · О нас · Подписка</p></nav>
<div class="sidebar"><p>Короткий анонс</p></div>
<article><h1>Заголовок</h1><p>` + para + `</p><p>` + para + `</p><script>var x = 1;</script><p>` + para + `</p></article>
<footer><p>© 2024 Все права защищены. Копирование материалов запрещено без согласия редакции.</p></footer></body></html>`
	got := Text(page)
	if strings.Count(got, "\n\n") != 2 || !strings.Contains(got, "длинный абзац") {
		t.Fatalf("article paragraphs missing: %q", got)
	}
	for _, bad := range []string{"Главная", "Все права", "var x", "анонс"} {
		if strings.Contains(got, bad) {
			t.Errorf("boilerplate %q leaked", bad)
		}
	}
}

func TestTextEmptyWhenNoArticle(t *testing.T) {
	if Text("<html><body><p>hi</p><div>menu</div></body></html>") != "" {
		t.Error("a page without a body must return an empty string")
	}
}
