package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseISODuration(t *testing.T) {
	for in, want := range map[string]int{"PT45S": 45, "PT1M5S": 65, "PT1H2M3S": 3723, "P1DT1H": 90000, "": 0, "junk": 0} {
		if got := parseISODuration(in); got != want {
			t.Errorf("parseISODuration(%q)=%d want %d", in, got, want)
		}
	}
}

func statsClient(t *testing.T, h http.HandlerFunc) *Client {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(func(context.Context) (string, error) { return "tok", nil })
	c.APIURL = srv.URL
	return c
}

func TestStatsCollectsChannelAndVideos(t *testing.T) {
	var paths []string
	c := statsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Error("owner token must authorise the request")
		}
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/channels":
			if r.URL.Query().Get("mine") != "true" {
				t.Error("channel must be selected with mine=true")
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"UC1","snippet":{"title":"A"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}},
				"statistics":{"viewCount":"100","hiddenSubscriberCount":true,"subscriberCount":"5","videoCount":"3"}}]}`))
		case "/playlistItems":
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = w.Write([]byte(`{"nextPageToken":"p2","items":[{"contentDetails":{"videoId":"a"}},{"contentDetails":{"videoId":"b"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"c"}},{"contentDetails":{"videoId":"d"}}]}`))
		case "/videos":
			if got := r.URL.Query().Get("id"); got != "a,b,c" {
				t.Errorf("videos requested=%q want the first 3 uploads", got)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"a","snippet":{"title":"T","publishedAt":"2026-01-02T03:04:05Z"},"contentDetails":{"duration":"PT30S"},
				"statistics":{"viewCount":"10","likeCount":"2","commentCount":"1"}},{"id":"b","snippet":{},"statistics":{"viewCount":"3"}}]}`))
		}
	})
	ch, vs, units, err := c.Stats(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths, " "); got != "/channels /playlistItems /playlistItems /videos" || units != 4 {
		t.Errorf("calls=%q units=%d", got, units)
	}
	if ch.ChannelID != "UC1" || ch.Subscribers != nil || ch.Views != 100 || ch.Videos != 3 {
		t.Errorf("hidden subscriber count must be nil: %+v", ch)
	}
	if len(vs) != 2 || vs[0].DurationSec != 30 || *vs[0].Likes != 2 || *vs[0].Comments != 1 || vs[1].Likes != nil || vs[1].Comments != nil {
		t.Errorf("videos: %+v", vs)
	}
}

func TestStatsRequiresExactlyOneChannel(t *testing.T) {
	c := statsClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"items":[]}`)) })
	if _, _, _, err := c.Stats(context.Background(), 10); err == nil {
		t.Fatal("no channel must be an error")
	}
}

func TestStatsQuotaError(t *testing.T) {
	c := statsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"quotaExceeded"}]}}`))
	})
	_, _, _, err := c.Stats(context.Background(), 10)
	if e, ok := err.(*APIError); !ok || e.Reason != "quotaExceeded" {
		t.Fatalf("want quota APIError, got %v", err)
	}
}
