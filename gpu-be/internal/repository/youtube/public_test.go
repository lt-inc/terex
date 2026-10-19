package youtube

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"terex/proto/workflow"
)

func TestShortsPlaylist(t *testing.T) {
	if got, ok := shortsPlaylist("UCabc123"); !ok || got != "UUSHabc123" {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := shortsPlaylist("PLabc"); ok {
		t.Fatal("only channel ids map to a shorts playlist")
	}
}

func TestChannelShortIDsUsesShortsPlaylistAndTreatsMissingAsEmpty(t *testing.T) {
	var playlist string
	c := statsClient(t, func(w http.ResponseWriter, r *http.Request) {
		playlist = r.URL.Query().Get("playlistId")
		if playlist == "UUSHnone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"playlistNotFound"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"a"}},{"contentDetails":{"videoId":"b"}}]}`))
	})
	ids, units, err := c.ChannelShortIDs(context.Background(), "UCxyz", 10)
	if err != nil || strings.Join(ids, ",") != "a,b" || units != 1 || playlist != "UUSHxyz" {
		t.Fatalf("ids=%v units=%d playlist=%q err=%v", ids, units, playlist, err)
	}
	ids, _, err = c.ChannelShortIDs(context.Background(), "UCnone", 10)
	if err != nil || len(ids) != 0 {
		t.Fatalf("a channel without Shorts must be an empty result: %v %v", ids, err)
	}
}

func TestVideosBatchesByFifty(t *testing.T) {
	calls := 0
	c := statsClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"items":[{"id":"v","snippet":{"channelId":"UC1","channelTitle":"C","title":"T"},"contentDetails":{"duration":"PT20S"},"statistics":{"viewCount":"7"}}]}`))
	})
	ids := make([]string, 120)
	for i := range ids {
		ids[i] = "id"
	}
	vs, units, err := c.Videos(context.Background(), ids)
	if err != nil || calls != 3 || units != 3 || len(vs) != 3 || vs[0].DurationSec != 20 || vs[0].Views != 7 || vs[0].ChannelID != "UC1" {
		t.Fatalf("calls=%d units=%d vs=%+v err=%v", calls, units, vs, err)
	}
}

func TestSearchShortsCostsOneHundredUnits(t *testing.T) {
	c := statsClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("order") != "viewCount" || q.Get("videoDuration") != "short" || q.Get("regionCode") != "US" {
			t.Errorf("unexpected query %v", q)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":{"videoId":"x"}},{"id":{"videoId":"y"}}]}`))
	})
	ids, units, err := c.SearchShorts(context.Background(), workflow.PublicVideosRequest{Query: "cats", RegionCode: "US"})
	if err != nil || len(ids) != 2 || units != 100 {
		t.Fatalf("ids=%v units=%d err=%v", ids, units, err)
	}
}
