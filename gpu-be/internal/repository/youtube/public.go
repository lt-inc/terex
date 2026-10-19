package youtube

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"terex/proto/workflow"
)

// searchCost is the quota price of one search.list call; every other list call costs one unit.
const searchCost = 100

// shortsPlaylist maps a channel id (UC...) to the hidden playlist that holds only its Shorts (UUSH...).
func shortsPlaylist(channelID string) (string, bool) {
	if len(channelID) < 3 || !strings.HasPrefix(channelID, "UC") {
		return "", false
	}
	return "UUSH" + channelID[2:], true
}

// ChannelShortIDs lists up to max Shorts of a public channel, newest first. A channel without
// Shorts has no such playlist: that is an empty result, not an error.
func (c *Client) ChannelShortIDs(ctx context.Context, channelID string, max int) ([]string, int, error) {
	var units int
	playlist, ok := shortsPlaylist(channelID)
	if !ok {
		return nil, 0, errors.New("invalid YouTube channel id")
	}
	var ids []string
	for token := ""; len(ids) < max; {
		var page struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ContentDetails struct {
					VideoID string `json:"videoId"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		q := url.Values{"part": {"contentDetails"}, "playlistId": {playlist}, "maxResults": {"50"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		if err := c.getJSON(ctx, &units, "/playlistItems", q, &page); err != nil {
			var api *APIError
			if errors.As(err, &api) && api.Status == 404 {
				return ids, units, nil
			}
			return ids, units, err
		}
		for _, p := range page.Items {
			ids = append(ids, p.ContentDetails.VideoID)
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	if len(ids) > max {
		ids = ids[:max]
	}
	return ids, units, nil
}

// Videos reads public statistics of ids in batches of 50 (one unit per batch).
func (c *Client) Videos(ctx context.Context, ids []string) ([]workflow.PublicVideo, int, error) {
	var units int
	var out []workflow.PublicVideo
	for len(ids) > 0 {
		n := min(statsBatch, len(ids))
		var page struct {
			Items []struct {
				ID      string `json:"id"`
				Snippet struct {
					ChannelID    string    `json:"channelId"`
					ChannelTitle string    `json:"channelTitle"`
					Title        string    `json:"title"`
					PublishedAt  time.Time `json:"publishedAt"`
				} `json:"snippet"`
				ContentDetails struct {
					Duration string `json:"duration"`
				} `json:"contentDetails"`
				Statistics struct {
					Views string  `json:"viewCount"`
					Likes *string `json:"likeCount"`
				} `json:"statistics"`
			} `json:"items"`
		}
		q := url.Values{"part": {"snippet,contentDetails,statistics"}, "id": {strings.Join(ids[:n], ",")}, "maxResults": {"50"}}
		if err := c.getJSON(ctx, &units, "/videos", q, &page); err != nil {
			return out, units, err
		}
		for _, v := range page.Items {
			out = append(out, workflow.PublicVideo{ID: v.ID, ChannelID: v.Snippet.ChannelID, ChannelTitle: v.Snippet.ChannelTitle, Title: v.Snippet.Title,
				PublishedAt: v.Snippet.PublishedAt, DurationSec: parseISODuration(v.ContentDetails.Duration), Views: count(v.Statistics.Views), Likes: optCount(v.Statistics.Likes)})
		}
		ids = ids[n:]
	}
	return out, units, nil
}

// SearchShorts returns ids of the most viewed short videos for a query.
func (c *Client) SearchShorts(ctx context.Context, q workflow.PublicVideosRequest) ([]string, int, error) {
	var units int
	v := url.Values{"part": {"id"}, "type": {"video"}, "videoDuration": {"short"}, "order": {"viewCount"}, "q": {q.Query},
		"maxResults": {"50"}}
	if q.RegionCode != "" {
		v.Set("regionCode", q.RegionCode)
	}
	if q.Language != "" {
		v.Set("relevanceLanguage", q.Language)
	}
	if !q.PublishedAfter.IsZero() {
		v.Set("publishedAfter", q.PublishedAfter.UTC().Format(time.RFC3339))
	}
	var page struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
		} `json:"items"`
	}
	err := c.getJSON(ctx, &units, "/search", v, &page)
	units += searchCost - 1 // getJSON counted one unit
	if err != nil {
		return nil, units, err
	}
	var ids []string
	for _, it := range page.Items {
		if it.ID.VideoID != "" {
			ids = append(ids, it.ID.VideoID)
		}
	}
	return ids, units, nil
}
