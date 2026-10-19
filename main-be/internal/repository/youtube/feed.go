// Package youtube reads public channel feeds (no API key needed).
package youtube

import (
	"context"
	"encoding/xml"
	"errors"
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
		return nil, &domain.FeedHTTPError{StatusCode: resp.StatusCode, URL: u}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// IsShort reports whether the video is a YouTube Short: /shorts/<id> answers 200 for a Short and
// redirects to /watch for any other video. The check needs no API quota.
func (f *Feed) IsShort(ctx context.Context, videoID string) (bool, error) {
	if !videoIDRe.MatchString(videoID) {
		return false, fmt.Errorf("invalid video id %q", videoID)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, "https://www.youtube.com/shorts/"+videoID, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (terex-monitor)")
	req.Header.Set("Cookie", "SOCS=CAI") // skips the EU consent redirect, which would look like "not a Short"
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if strings.Contains(resp.Header.Get("Location"), "consent.") {
			return false, errors.New("YouTube asked for cookie consent")
		}
		return false, nil
	}
	return false, &domain.FeedHTTPError{StatusCode: resp.StatusCode, URL: "shorts/" + videoID}
}

var videoIDRe = regexp.MustCompile(`^[\w-]{11}$`)
