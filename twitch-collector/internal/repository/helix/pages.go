package helix

import (
	"context"
	"net/url"
	"strings"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

type channelMetadata struct {
	Login, Language string
	Expires         time.Time
}
type clipsResponse struct {
	Data []struct {
		ID              string    `json:"id"`
		BroadcasterID   string    `json:"broadcaster_id"`
		BroadcasterName string    `json:"broadcaster_name"`
		Title           string    `json:"title"`
		Views           int       `json:"view_count"`
		Created         time.Time `json:"created_at"`
		Duration        float64   `json:"duration"`
		VideoID         string    `json:"video_id"`
		Offset          *float64  `json:"vod_offset"`
		GameID          string    `json:"game_id"`
		Thumbnail       string    `json:"thumbnail_url"`
		Language        string    `json:"language"`
	} `json:"data"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}

func (c *Client) GetClipPage(ctx context.Context, f twitch.Flow, ch domain.Channel, w twitch.DiscoveryWindow) (domain.ClipPage, error) {
	q := url.Values{"first": {"100"}, "started_at": {w.Start.UTC().Format(time.RFC3339Nano)}, "ended_at": {w.End.UTC().Format(time.RFC3339Nano)}}
	if f.SourceMode == "category" {
		q.Set("game_id", f.CategoryID)
	} else {
		q.Set("broadcaster_id", ch.BroadcasterID)
	}
	if w.Cursor != "" {
		q.Set("after", w.Cursor)
	}
	return c.clipPage(ctx, q, f, ch)
}
func (c *Client) GetClipsByIDs(ctx context.Context, ids []string) ([]domain.Clip, error) {
	q := url.Values{}
	for _, id := range ids {
		if ClipIDPattern.MatchString(id) {
			q.Add("id", id)
		}
	}
	if len(q["id"]) == 0 {
		return []domain.Clip{}, nil
	}
	page, err := c.clipPage(ctx, q, twitch.Flow{SourceMode: "category"}, domain.Channel{})
	return page.Clips, err
}
func (c *Client) clipPage(ctx context.Context, q url.Values, f twitch.Flow, ch domain.Channel) (domain.ClipPage, error) {
	var body clipsResponse
	if err := c.get(ctx, "/clips", q, &body); err != nil {
		return domain.ClipPage{}, err
	}
	at := time.Now().UTC()
	out := domain.ClipPage{Clips: []domain.Clip{}, Cursor: body.Pagination.Cursor, Count: len(body.Data)}
	if len(body.Data) > 0 {
		out.LastViews = body.Data[len(body.Data)-1].Views
	}
	missing := url.Values{}
	var metadataErr error
	seen := map[string]bool{}
	c.mu.Lock()
	if c.channelCache == nil {
		c.channelCache = map[string]channelMetadata{}
	}
	for _, v := range body.Data {
		meta, ok := c.channelCache[v.BroadcasterID]
		if (!ok || !at.Before(meta.Expires)) && !seen[v.BroadcasterID] {
			missing.Add("broadcaster_id", v.BroadcasterID)
			seen[v.BroadcasterID] = true
		}
	}
	c.mu.Unlock()
	if len(missing) > 0 {
		var info struct {
			Data []struct {
				ID       string `json:"broadcaster_id"`
				Login    string `json:"broadcaster_login"`
				Language string `json:"broadcaster_language"`
			} `json:"data"`
		}
		// A failed optional lookup must not discard successfully fetched clips.
		if metadataErr = c.get(ctx, "/channels", missing, &info); metadataErr == nil {
			c.mu.Lock()
			for _, v := range info.Data {
				c.channelCache[v.ID] = channelMetadata{v.Login, v.Language, at.Add(time.Hour)}
			}
			c.mu.Unlock()
		}
	}
	unique := map[string]bool{}
	for _, v := range body.Data {
		if !ClipIDPattern.MatchString(v.ID) || v.BroadcasterID == "" || unique[v.ID] || (f.SourceMode != "category" && v.BroadcasterID != ch.BroadcasterID) {
			continue
		}
		unique[v.ID] = true
		login := v.BroadcasterName
		language := strings.ToLower(strings.TrimSpace(v.Language))
		languageSource := "clip"
		if language == "" || language == "other" {
			languageSource = "unknown"
		}
		c.mu.Lock()
		meta := c.channelCache[v.BroadcasterID]
		c.mu.Unlock()
		if meta.Login != "" {
			login = meta.Login
		}
		if login == "" && ch.BroadcasterID == v.BroadcasterID {
			login = ch.Login
		}
		if !LoginPattern.MatchString(login) {
			if metadataErr != nil {
				return domain.ClipPage{}, metadataErr
			}
			continue
		}
		if language == "" && meta.Language != "" {
			language = strings.ToLower(meta.Language)
			languageSource = "channel"
		}
		out.Clips = append(out.Clips, domain.Clip{ID: v.ID, ChannelID: ch.ID, BroadcasterID: v.BroadcasterID, BroadcasterName: login, Title: v.Title, ViewCount: v.Views, CreatedAt: v.Created, ObservedAt: at, Duration: v.Duration, VideoID: v.VideoID, VODOffset: v.Offset, GameID: v.GameID, Language: language, LanguageSource: languageSource, ThumbnailURL: v.Thumbnail, URL: "https://clips.twitch.tv/" + v.ID, State: "discovered"})
	}
	return out, nil
}
func (c *Client) ReportDiscovery(report domain.DiscoveryReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.discoveries == nil {
		c.discoveries = map[string]domain.DiscoveryReport{}
	}
	if len(c.discoveries) >= 500 {
		var oldest string
		var at time.Time
		for k, v := range c.discoveries {
			if oldest == "" || v.CheckedAt.Before(at) {
				oldest, at = k, v.CheckedAt
			}
		}
		delete(c.discoveries, oldest)
	}
	c.discoveries[report.FlowID+":"+report.BroadcasterID] = report
}
