// Package youtube reads public channel feeds (no API key needed).
package youtube

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"terex/main-be/internal/domain"
)

type Feed struct{ HTTP *http.Client }

func New() *Feed { return &Feed{HTTP: &http.Client{Timeout: 20 * time.Second}} }

var (
	channelIDRe = regexp.MustCompile(`^UC[\w-]{22}$`)
	pageIDRe    = regexp.MustCompile(`"(?:channelId|externalId)":"(UC[\w-]{22})"`)
)

// ResolveChannel accepts a channel id, /channel/UC... URL, or a @handle URL and returns the UC id.
func (f *Feed) ResolveChannel(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if channelIDRe.MatchString(raw) {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid channel url %q", raw)
	}
	for _, part := range strings.Split(u.Path, "/") {
		if channelIDRe.MatchString(part) {
			return part, nil
		}
	}
	body, err := f.get(ctx, raw)
	if err != nil {
		return "", err
	}
	if m := pageIDRe.FindSubmatch(body); m != nil {
		return string(m[1]), nil
	}
	return "", fmt.Errorf("cannot resolve channel id from %q", raw)
}

type atom struct {
	Entries []struct {
		VideoID string `xml:"videoId"`
		Title   string `xml:"title"`
	} `xml:"entry"`
}

func (f *Feed) Latest(ctx context.Context, channelID string) ([]domain.FeedVideo, error) {
	body, err := f.get(ctx, "https://www.youtube.com/feeds/videos.xml?channel_id="+url.QueryEscape(channelID))
	if err != nil {
		return nil, err
	}
	var a atom
	if err := xml.Unmarshal(body, &a); err != nil {
		return nil, err
	}
	out := make([]domain.FeedVideo, 0, len(a.Entries))
	for _, e := range a.Entries {
		out = append(out, domain.FeedVideo{ID: e.VideoID, Title: e.Title, URL: "https://www.youtube.com/watch?v=" + e.VideoID})
	}
	return out, nil
}

func (f *Feed) get(ctx context.Context, u string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (terex-monitor)")
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
