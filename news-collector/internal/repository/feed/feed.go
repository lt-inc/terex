// Package feed downloads and parses RSS 2.0 / Atom feeds.
package feed

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Item struct {
	Title     string
	Link      string
	Summary   string // plain text taken from the feed (description / content)
	Publisher string // outlet name when the feed carries one (Google News <source>)
	Published time.Time
}

type Feed struct {
	Title string
	Items []Item
}

type Client struct {
	HTTP      *http.Client
	UserAgent string
}

const maxFeedBytes = 8 << 20

// GNewsURL builds a Google News search feed for a query in the given language.
func GNewsURL(query, lang string) string {
	hl, gl, ceid := "ru", "RU", "RU:ru"
	if lang == "en" {
		hl, gl, ceid = "en-US", "US", "US:en"
	}
	v := url.Values{"q": {query + " when:2d"}, "hl": {hl}, "gl": {gl}, "ceid": {ceid}}
	return "https://news.google.com/rss/search?" + v.Encode()
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (*Feed, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid feed URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	ua := c.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (compatible; TerexNewsCollector/1.0)"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: %w", unwrap(err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxFeedBytes))
	if err != nil {
		return nil, fmt.Errorf("feed read failed: %w", unwrap(err))
	}
	return Parse(data)
}

func unwrap(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

type rssDoc struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			GUID        string `xml:"guid"`
			Description string `xml:"description"`
			Encoded     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
			PubDate     string `xml:"pubDate"`
			Source      string `xml:"source"`
			DCDate      string `xml:"http://purl.org/dc/elements/1.1/ date"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDoc struct {
	Title   string `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
	} `xml:"entry"`
}

// Parse accepts RSS 2.0, RSS 1.0 style items inside channel, and Atom.
func Parse(data []byte) (*Feed, error) {
	var root struct{ XMLName xml.Name }
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("not an XML feed")
	}
	out := &Feed{}
	switch strings.ToLower(root.XMLName.Local) {
	case "rss", "rdf":
		var doc rssDoc
		d := xml.NewDecoder(strings.NewReader(string(data)))
		d.Strict = false
		d.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
		if err := d.Decode(&doc); err != nil {
			return nil, fmt.Errorf("invalid RSS: %w", err)
		}
		out.Title = clean(doc.Channel.Title)
		for _, it := range doc.Channel.Items {
			link := strings.TrimSpace(it.Link)
			if link == "" && strings.HasPrefix(strings.TrimSpace(it.GUID), "http") {
				link = strings.TrimSpace(it.GUID)
			}
			body := it.Encoded
			if body == "" {
				body = it.Description
			}
			date := it.PubDate
			if date == "" {
				date = it.DCDate
			}
			title, publisher := clean(it.Title), clean(it.Source)
			if publisher != "" { // Google News appends " - Outlet" to every title
				title = strings.TrimSuffix(title, " - "+publisher)
			}
			out.Items = append(out.Items, Item{Title: title, Link: link, Summary: clean(body), Published: parseTime(date), Publisher: publisher})
		}
	case "feed":
		var doc atomDoc
		d := xml.NewDecoder(strings.NewReader(string(data)))
		d.Strict = false
		d.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
		if err := d.Decode(&doc); err != nil {
			return nil, fmt.Errorf("invalid Atom: %w", err)
		}
		out.Title = clean(doc.Title)
		for _, e := range doc.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			body := e.Content
			if body == "" {
				body = e.Summary
			}
			date := e.Published
			if date == "" {
				date = e.Updated
			}
			out.Items = append(out.Items, Item{Title: clean(e.Title), Link: strings.TrimSpace(link), Summary: clean(body), Published: parseTime(date)})
		}
	default:
		return nil, fmt.Errorf("not an RSS or Atom feed")
	}
	items := out.Items[:0]
	for _, it := range out.Items {
		if it.Title != "" && it.Link != "" {
			items = append(items, it)
		}
	}
	out.Items = items
	return out, nil
}

var tags = regexp.MustCompile(`(?s)<[^>]*>`)
var spaces = regexp.MustCompile(`\s+`)

// clean strips markup and entities, collapsing whitespace.
func clean(s string) string {
	s = tags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

var layouts = []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
