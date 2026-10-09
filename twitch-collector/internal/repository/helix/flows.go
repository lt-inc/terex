package helix

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func (c *Client) ResolveBroadcaster(ctx context.Context, id string) (domain.Channel, error) {
	var body struct {
		Data []struct {
			ID, Login   string
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/users", url.Values{"id": {id}}, &body); err != nil {
		return domain.Channel{}, err
	}
	if len(body.Data) != 1 {
		return domain.Channel{}, domain.ErrNotFound
	}
	u := body.Data[0]
	return domain.Channel{BroadcasterID: u.ID, Login: u.Login, DisplayName: u.DisplayName}, nil
}

func (c *Client) Categories(ctx context.Context, query string) ([]twitch.Category, error) {
	out := []twitch.Category{}
	if strings.TrimSpace(query) == "" {
		return out, nil
	}
	var body struct {
		Data []twitch.Category `json:"data"`
	}
	if err := c.get(ctx, "/search/categories", url.Values{"query": {query}, "first": {"20"}}, &body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

// Helix sorts clips by views. The bounded pagination works for a broadcaster
// or a category. Only the clip's own language is authoritative; current channel
// metadata is a labelled hint and cannot admit historical clips to a language flow.
func (c *Client) GetFlowClips(ctx context.Context, flow twitch.Flow, ch domain.Channel, start, end time.Time, desired int, known map[string]bool) ([]domain.Clip, error) {
	q := url.Values{"first": {"100"}, "started_at": {start.UTC().Format(time.RFC3339)}, "ended_at": {end.UTC().Format(time.RFC3339)}}
	if flow.SourceMode == "category" {
		q.Set("game_id", flow.CategoryID)
	} else {
		q.Set("broadcaster_id", ch.BroadcasterID)
	}
	out := []domain.Clip{}
	report := domain.DiscoveryReport{FlowID: flow.ID, BroadcasterID: ch.BroadcasterID, Language: flow.Language, CheckedAt: time.Now().UTC()}
	defer func() {
		report.Fetched = len(out)
		for _, v := range out {
			if flow.Language == "" || v.LanguageSource == "clip" && v.Language == flow.Language {
				report.LanguageMatched++
			} else if v.LanguageSource != "clip" {
				report.LanguageUnknown++
			} else {
				report.LanguageRejected++
			}
		}
		c.mu.Lock()
		if c.discoveries == nil {
			c.discoveries = make(map[string]domain.DiscoveryReport)
		}
		// Keep the operator view bounded even after many deleted flows/sources.
		if len(c.discoveries) >= 500 {
			var oldestKey string
			var oldest time.Time
			for key, v := range c.discoveries {
				if oldestKey == "" || v.CheckedAt.Before(oldest) {
					oldestKey, oldest = key, v.CheckedAt
				}
			}
			delete(c.discoveries, oldestKey)
		}
		c.discoveries[flow.ID+":"+ch.BroadcasterID] = report
		c.mu.Unlock()
		if report.PageLimitReached {
			slog.InfoContext(ctx, "collector discovery page limit reached", "flow_id", flow.ID, "language", flow.Language, "pages", report.Pages, "fetched", report.Fetched, "language_matched", report.LanguageMatched, "language_rejected", report.LanguageRejected, "language_unknown", report.LanguageUnknown)
		}
	}()
	seen := map[string]bool{}
	cursors := map[string]bool{}
	for page := 0; page < 5; page++ {
		var body struct {
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
		if err := c.get(ctx, "/clips", q, &body); err != nil {
			report.Error = "Не удалось получить страницу клипов Twitch"
			return nil, err
		}
		report.Pages++
		current := []domain.Clip{}
		broadcasters := url.Values{}
		bseen := map[string]bool{}
		for _, v := range body.Data {
			if !ClipIDPattern.MatchString(v.ID) || seen[v.ID] || v.BroadcasterID == "" {
				continue
			}
			if flow.SourceMode != "category" && v.BroadcasterID != ch.BroadcasterID {
				continue
			}
			seen[v.ID] = true
			language := strings.ToLower(strings.TrimSpace(v.Language))
			languageSource := "clip"
			if language == "" || language == "other" {
				languageSource = "unknown"
			}
			current = append(current, domain.Clip{ID: v.ID, ChannelID: ch.ID, BroadcasterID: v.BroadcasterID, BroadcasterName: v.BroadcasterName, Title: v.Title, ViewCount: v.Views, CreatedAt: v.Created, ObservedAt: time.Now().UTC(), Duration: v.Duration, VideoID: v.VideoID, VODOffset: v.Offset, GameID: v.GameID, Language: language, LanguageSource: languageSource, ThumbnailURL: v.Thumbnail, URL: "https://clips.twitch.tv/" + v.ID, State: "discovered"})
			if !bseen[v.BroadcasterID] {
				broadcasters.Add("broadcaster_id", v.BroadcasterID)
				bseen[v.BroadcasterID] = true
			}
		}
		if len(current) > 0 {
			var info struct {
				Data []struct {
					ID       string `json:"broadcaster_id"`
					Login    string `json:"broadcaster_login"`
					Language string `json:"broadcaster_language"`
				} `json:"data"`
			}
			if err := c.get(ctx, "/channels", broadcasters, &info); err != nil {
				report.Error = "Не удалось получить сведения о Twitch-каналах"
				return nil, fmt.Errorf("broadcaster metadata: %w", err)
			}
			for i := range current {
				for _, b := range info.Data {
					if current[i].BroadcasterID == b.ID {
						if current[i].Language == "" {
							current[i].Language = strings.ToLower(strings.TrimSpace(b.Language))
							current[i].LanguageSource = "channel"
						}
						current[i].BroadcasterName = b.Login
						break
					}
				}
			}
		}
		// A broadcaster missing from /channels (deleted or banned) keeps its
		// display name, which may not be a login. Terex rejects such a source
		// with HTTP 400 and that aborts the whole check, so drop the clip here.
		valid := current[:0]
		for _, v := range current {
			if LoginPattern.MatchString(v.BroadcasterName) {
				valid = append(valid, v)
			}
		}
		current = valid
		out = append(out, current...)
		eligible := 0
		for _, v := range out {
			if !known[v.ID] && (flow.Language == "" || v.LanguageSource == "clip" && v.Language == flow.Language) && v.ViewCount >= flow.MinViews && v.Duration >= flow.MinDurationSec && v.Duration <= flow.MaxDurationSec && end.Sub(v.CreatedAt) >= time.Duration(flow.MinAgeMinutes)*time.Minute {
				eligible++
			}
		}
		if desired > 0 && eligible >= desired {
			break
		}
		cursor := body.Pagination.Cursor
		if cursor == "" || cursors[cursor] || len(body.Data) == 0 || body.Data[len(body.Data)-1].Views < flow.MinViews {
			break
		}
		cursors[cursor] = true
		q.Set("after", cursor)
		if page == 4 {
			report.PageLimitReached = true
		}
	}
	return out, nil
}

func (c *Client) DiscoveryReports() []domain.DiscoveryReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]domain.DiscoveryReport, 0, len(c.discoveries))
	for _, report := range c.discoveries {
		out = append(out, report)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FlowID != out[j].FlowID {
			return out[i].FlowID < out[j].FlowID
		}
		return out[i].BroadcasterID < out[j].BroadcasterID
	})
	return out
}
