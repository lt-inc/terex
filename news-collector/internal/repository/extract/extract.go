// Package extract downloads an article page and pulls out its main text.
// It is a small paragraph-density heuristic: the container holding the most
// paragraph text wins. Pages it cannot read return an empty string, and the caller
// falls back to the feed summary.
package extract

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const (
	maxPageBytes = 3 << 20
	MaxTextChars = 12000
)

type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// Page is what could be read from an article page.
type Page struct {
	Text  string
	Image string // og:image / twitter:image, absolute URL
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (Page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Page{}, err
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (compatible; TerexNewsCollector/1.0)"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("page unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("page returned HTTP %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
		return Page{}, fmt.Errorf("page is not HTML")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxPageBytes))
	if err != nil {
		return Page{}, fmt.Errorf("page read failed")
	}
	return Page{Text: Text(string(data)), Image: Image(string(data), rawURL)}, nil
}

var skipTags = map[string]bool{"script": true, "style": true, "noscript": true, "nav": true, "header": true, "footer": true, "aside": true, "form": true, "iframe": true, "svg": true, "button": true}

type scored struct {
	score int
}

// Text returns the main article text of an HTML document, or "".
func Text(doc string) string {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	scores := map[*html.Node]*scored{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if skipTags[n.Data] {
				return
			}
			if n.Data == "p" {
				if l := len([]rune(strings.TrimSpace(nodeText(n)))); l >= 40 {
					if p := n.Parent; p != nil {
						add(scores, p, l)
						if g := p.Parent; g != nil {
							add(scores, g, l/2)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	var best *html.Node
	bestScore := 0
	for n, s := range scores {
		if s.score > bestScore {
			best, bestScore = n, s.score
		}
	}
	if best == nil || bestScore < 200 {
		return ""
	}
	var paras []string
	var collect func(n *html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		if n.Type == html.ElementNode && n.Data == "p" {
			if t := strings.TrimSpace(nodeText(n)); len([]rune(t)) >= 25 {
				paras = append(paras, t)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(best)
	out := strings.Join(paras, "\n\n")
	if r := []rune(out); len(r) > MaxTextChars {
		out = string(r[:MaxTextChars])
	}
	return out
}

func add(m map[*html.Node]*scored, n *html.Node, v int) {
	if n.Type != html.ElementNode {
		return
	}
	s := m[n]
	if s == nil {
		s = &scored{}
		m[n] = s
	}
	s.score += v
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var rec func(n *html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// Image returns the page's social-preview picture as an absolute http(s) URL, or "".
func Image(doc, base string) string {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	found := map[string]string{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			var key, content string
			for _, a := range n.Attr {
				switch a.Key {
				case "property", "name":
					key = strings.ToLower(a.Val)
				case "content":
					content = strings.TrimSpace(a.Val)
				}
			}
			if content != "" && found[key] == "" {
				found[key] = content
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	for _, key := range []string{"og:image:secure_url", "og:image", "og:image:url", "twitter:image", "twitter:image:src"} {
		if v := found[key]; v != "" {
			if u, err := b.Parse(v); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
				return u.String()
			}
		}
	}
	return ""
}
