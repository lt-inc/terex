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

func TestImage(t *testing.T) {
	page := `<html><head><meta property="og:title" content="x"><meta property="og:image" content="/img/cover.jpg"><meta name="twitter:image" content="https://other/t.jpg"></head><body></body></html>`
	if got := Image(page, "https://news.example.com/a/b"); got != "https://news.example.com/img/cover.jpg" {
		t.Errorf("relative og:image must be resolved: %q", got)
	}
	if got := Image(`<meta name="twitter:image" content="https://cdn.x/t.png">`, "https://x"); got != "https://cdn.x/t.png" {
		t.Errorf("twitter:image fallback: %q", got)
	}
	if Image(`<meta property="og:image" content="javascript:alert(1)">`, "https://x") != "" || Image("<p>none</p>", "https://x") != "" {
		t.Error("unsafe or missing images must return an empty string")
	}
}
