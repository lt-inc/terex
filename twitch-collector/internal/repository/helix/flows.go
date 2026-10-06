package helix

import (
	"context"
	"fmt"
	"net/url"
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
// or a category; clip language is retained, with channel metadata as a fallback.
func (c *Client) GetFlowClips(ctx context.Context, flow twitch.Flow, ch domain.Channel, start, end time.Time, desired int, known map[string]bool) ([]domain.Clip, error) {
	q := url.Values{"first": {"100"}, "started_at": {start.UTC().Format(time.RFC3339)}, "ended_at": {end.UTC().Format(time.RFC3339)}}
	if flow.SourceMode == "category" {
		q.Set("game_id", flow.CategoryID)
	} else {
		q.Set("broadcaster_id", ch.BroadcasterID)
	}
	out := []domain.Clip{}
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
			return nil, err
		}
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
			current = append(current, domain.Clip{ID: v.ID, ChannelID: ch.ID, BroadcasterID: v.BroadcasterID, BroadcasterName: v.BroadcasterName, Title: v.Title, ViewCount: v.Views, CreatedAt: v.Created, Duration: v.Duration, VideoID: v.VideoID, VODOffset: v.Offset, GameID: v.GameID, Language: v.Language, ThumbnailURL: v.Thumbnail, URL: "https://clips.twitch.tv/" + v.ID, State: "discovered"})
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
				return nil, fmt.Errorf("broadcaster metadata: %w", err)
			}
			for i := range current {
				for _, b := range info.Data {
					if current[i].BroadcasterID == b.ID {
						if current[i].Language == "" {
							current[i].Language = b.Language
						}
						current[i].BroadcasterName = b.Login
						break
					}
				}
			}
		}
		out = append(out, current...)
		eligible := 0
		for _, v := range out {
			if !known[v.ID] && (flow.Language == "" || v.Language == flow.Language) && v.ViewCount >= flow.MinViews && v.Duration >= flow.MinDurationSec && v.Duration <= flow.MaxDurationSec && end.Sub(v.CreatedAt) >= time.Duration(flow.MinAgeMinutes)*time.Minute {
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
	}
	return out, nil
}
