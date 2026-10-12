package service

import (
	"html"
	"regexp"
	"strings"

	"terex/news-collector/internal/domain"
)

// Post text uses a small markup that is safe to store and easy to edit by hand:
//
//	**bold**   __italic__   "- item" or "• item" bullets   "> quote" lines
//
// Everything is HTML-escaped first and only then turned into the few tags Telegram accepts,
// so neither the model nor an editor can inject markup of their own.
var (
	boldRe   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe = regexp.MustCompile(`__(.+?)__`)
)

func inlineHTML(escaped string) string {
	s := boldRe.ReplaceAllString(escaped, "<b>$1</b>")
	s = italicRe.ReplaceAllString(s, "<i>$1</i>")
	return strings.ReplaceAll(strings.ReplaceAll(s, "**", ""), "__", "") // unmatched markers are dropped
}

// markupToHTML renders the post body.
func markupToHTML(text string) string {
	var out []string
	var quote []string
	flush := func() {
		if len(quote) > 0 {
			out = append(out, "<blockquote>"+strings.Join(quote, "\n")+"</blockquote>")
			quote = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "> "):
			quote = append(quote, inlineHTML(html.EscapeString(strings.TrimPrefix(trimmed, "> "))))
			continue
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "), strings.HasPrefix(trimmed, "• "):
			flush()
			_, rest, _ := strings.Cut(trimmed, " ")
			out = append(out, "• "+inlineHTML(html.EscapeString(rest)))
			continue
		}
		flush()
		out = append(out, inlineHTML(html.EscapeString(line)))
	}
	flush()
	return strings.Join(out, "\n")
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainLength is the number of characters Telegram counts for a message or caption.
func plainLength(htmlText string) int {
	return len([]rune(html.UnescapeString(tagRe.ReplaceAllString(htmlText, ""))))
}

const (
	telegramLimit = 4096
	captionLimit  = 1024
)

// RenderOptions are the per-flow parts of the layout.
type RenderOptions struct {
	Lang   string
	Footer string
}

// RenderHTML builds the Telegram message:
//
//	<emoji> <b>headline</b>
//	body (bold accents, bullets, quotes)
//	#hashtags
//	Sources: a · b
//	photo credit
//	footer
func RenderHTML(p domain.Post, o RenderOptions) string {
	head := ""
	if p.Title != "" {
		head = "<b>" + html.EscapeString(p.Title) + "</b>"
		if p.Emoji != "" {
			head = html.EscapeString(p.Emoji) + " " + head
		}
	}
	var tail []string
	if p.Hashtags != "" {
		tail = append(tail, html.EscapeString(p.Hashtags))
	}
	if len(p.Links) > 0 {
		label := "Источники: "
		if o.Lang == "en" {
			label = "Sources: "
		}
		parts := make([]string, 0, len(p.Links))
		for _, l := range p.Links {
			parts = append(parts, `<a href="`+html.EscapeString(l.URL)+`">`+html.EscapeString(l.Title)+`</a>`)
		}
		tail = append(tail, label+strings.Join(parts, " · "))
	}
	if p.ImageCredit != "" {
		credit := html.EscapeString(p.ImageCredit)
		if p.ImageCreditURL != "" {
			credit = `<a href="` + html.EscapeString(p.ImageCreditURL) + `">` + credit + `</a>`
		}
		label := "Фото: "
		if o.Lang == "en" {
			label = "Photo: "
		}
		tail = append(tail, label+credit+" / Pexels")
	}
	if f := strings.TrimSpace(o.Footer); f != "" {
		tail = append(tail, html.EscapeString(f))
	}
	tailText := strings.Join(tail, "\n\n")

	// Telegram counts rendered characters, not markup; keep well under the limit.
	room := telegramLimit - plainLength(head) - plainLength(tailText) - 200
	body := markupToHTML(trimToLimit(p.Text, max(room, 200)))

	parts := make([]string, 0, 3)
	for _, s := range []string{head, body, tailText} {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}
