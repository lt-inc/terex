package feed

import (
	"strings"
	"testing"
)

const rss = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Test &amp; Co</title>
<item><title>Headline one - Outlet</title><link>https://e.com/1</link><description>&lt;p&gt;Summary &amp;amp; more&lt;/p&gt;</description><pubDate>Mon, 02 Jan 2006 15:04:05 +0300</pubDate><source url="https://o.com">Outlet</source></item>
<item><title>No link</title></item>
<item><title>Body</title><guid>https://e.com/3</guid><content:encoded><![CDATA[<p>Full <b>text</b></p>]]></content:encoded></item>
</channel></rss>`

const atom = `<feed xmlns="http://www.w3.org/2005/Atom"><title>A</title>
<entry><title>Entry</title><link rel="alternate" href="https://e.com/a"/><summary>S</summary><updated>2024-05-01T10:00:00Z</updated></entry></feed>`

func TestParseRSS(t *testing.T) {
	f, err := Parse([]byte(rss))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Test & Co" || len(f.Items) != 2 {
		t.Fatalf("%+v", f)
	}
	one := f.Items[0]
	if one.Title != "Headline one" || one.Publisher != "Outlet" || one.Summary != "Summary & more" || one.Published.IsZero() || one.Published.Hour() != 12 {
		t.Errorf("%+v", one)
	}
	if f.Items[1].Link != "https://e.com/3" || f.Items[1].Summary != "Full text" {
		t.Errorf("guid link or content:encoded not used: %+v", f.Items[1])
	}
}

func TestParseAtomAndErrors(t *testing.T) {
	f, err := Parse([]byte(atom))
	if err != nil || len(f.Items) != 1 || f.Items[0].Link != "https://e.com/a" || f.Items[0].Published.Year() != 2024 {
		t.Fatalf("%v %+v", err, f)
	}
	if _, err = Parse([]byte("<html><body>hi</body></html>")); err == nil {
		t.Error("HTML must not parse as a feed")
	}
	if _, err = Parse([]byte("not xml")); err == nil {
		t.Error("garbage must fail")
	}
}

func TestGNewsURL(t *testing.T) {
	ru := GNewsURL("биткоин", "ru")
	if !strings.Contains(ru, "hl=ru") || !strings.Contains(ru, "ceid=RU%3Aru") || !strings.Contains(ru, "when%3A2d") {
		t.Error(ru)
	}
	if !strings.Contains(GNewsURL("ai", "en"), "gl=US") {
		t.Error("English edition expected")
	}
}
