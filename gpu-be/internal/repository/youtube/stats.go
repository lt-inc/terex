package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"terex/proto/workflow"
)

const statsBatch = 50 // YouTube list endpoints return at most 50 items per call

type thumbs map[string]struct {
	URL string `json:"url"`
}

func (t thumbs) pick() string {
	for _, k := range []string{"medium", "default", "high"} {
		if v, ok := t[k]; ok {
			return v.URL
		}
	}
	return ""
}

func count(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

func optCount(s *string) *int64 {
	if s == nil {
		return nil
	}
	n := count(*s)
	return &n
}

var isoDuration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseISODuration converts "PT1M5S" to seconds; 0 when invalid.
func parseISODuration(s string) int {
	m := isoDuration.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	at := func(i int) int { n, _ := strconv.Atoi(m[i]); return n }
	return at(1)*86400 + at(2)*3600 + at(3)*60 + at(4)
}

func (c *Client) getJSON(ctx context.Context, units *int, path string, q url.Values, out any) error {
	*units++
	r, err := c.request(ctx, http.MethodGet, c.APIURL+path+"?"+q.Encode(), nil, nil)
	if err != nil {
		return err
	}
	if r.StatusCode != http.StatusOK {
		return apiError(r)
	}
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(out)
}

// Stats reads the connected channel and its latest uploads (private and unlisted
// included, because the call is authorised as the channel owner). Every list call
// costs one quota unit; the total is returned.
func (c *Client) Stats(ctx context.Context, maxVideos int) (workflow.ChannelStats, []workflow.VideoStats, int, error) {
	var units int
	var ch struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title      string `json:"title"`
				CustomURL  string `json:"customUrl"`
				Thumbnails thumbs `json:"thumbnails"`
			} `json:"snippet"`
			ContentDetails struct {
				RelatedPlaylists struct {
					Uploads string `json:"uploads"`
				} `json:"relatedPlaylists"`
			} `json:"contentDetails"`
			Statistics struct {
				Views  string  `json:"viewCount"`
				Subs   *string `json:"subscriberCount"`
				Hidden bool    `json:"hiddenSubscriberCount"`
				Videos string  `json:"videoCount"`
			} `json:"statistics"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, &units, "/channels", url.Values{"part": {"snippet,contentDetails,statistics"}, "mine": {"true"}}, &ch); err != nil {
		return workflow.ChannelStats{}, nil, units, err
	}
	if len(ch.Items) != 1 {
		return workflow.ChannelStats{}, nil, units, fmt.Errorf("OAuth connection must select exactly one YouTube channel")
	}
	it := ch.Items[0]
	out := workflow.ChannelStats{ChannelID: it.ID, Title: it.Snippet.Title, CustomURL: it.Snippet.CustomURL, ThumbnailURL: it.Snippet.Thumbnails.pick(),
		Views: count(it.Statistics.Views), Videos: count(it.Statistics.Videos)}
	if !it.Statistics.Hidden {
		out.Subscribers = optCount(it.Statistics.Subs)
	}

	var ids []string
	for token := ""; len(ids) < maxVideos && it.ContentDetails.RelatedPlaylists.Uploads != ""; {
		var page struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ContentDetails struct {
					VideoID string `json:"videoId"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		q := url.Values{"part": {"contentDetails"}, "playlistId": {it.ContentDetails.RelatedPlaylists.Uploads}, "maxResults": {"50"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		if err := c.getJSON(ctx, &units, "/playlistItems", q, &page); err != nil {
			return out, nil, units, err
		}
		for _, p := range page.Items {
			ids = append(ids, p.ContentDetails.VideoID)
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	if len(ids) > maxVideos {
		ids = ids[:maxVideos]
	}

	var videos []workflow.VideoStats
	for len(ids) > 0 {
		n := min(statsBatch, len(ids))
		var page struct {
			Items []struct {
				ID      string `json:"id"`
				Snippet struct {
					Title       string    `json:"title"`
					PublishedAt time.Time `json:"publishedAt"`
					Thumbnails  thumbs    `json:"thumbnails"`
				} `json:"snippet"`
				ContentDetails struct {
					Duration string `json:"duration"`
				} `json:"contentDetails"`
				Statistics struct {
					Views    string  `json:"viewCount"`
					Likes    *string `json:"likeCount"`
					Comments *string `json:"commentCount"`
				} `json:"statistics"`
			} `json:"items"`
		}
		q := url.Values{"part": {"snippet,contentDetails,statistics"}, "id": {strings.Join(ids[:n], ",")}, "maxResults": {"50"}}
		if err := c.getJSON(ctx, &units, "/videos", q, &page); err != nil {
			return out, videos, units, err
		}
		for _, v := range page.Items {
			videos = append(videos, workflow.VideoStats{ID: v.ID, Title: v.Snippet.Title, ThumbnailURL: v.Snippet.Thumbnails.pick(), PublishedAt: v.Snippet.PublishedAt,
				DurationSec: parseISODuration(v.ContentDetails.Duration), Views: count(v.Statistics.Views), Likes: optCount(v.Statistics.Likes), Comments: optCount(v.Statistics.Comments)})
		}
		ids = ids[n:]
	}
	return out, videos, units, nil
}
