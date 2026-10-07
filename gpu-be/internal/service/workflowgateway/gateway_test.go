package workflowgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/credentials"
	"terex/gpu-be/internal/repository/youtube"
	"terex/gpu-be/internal/service/googleauth"
	"terex/proto/workflow"
)

func TestLegacySRTMultilineAndTimes(t *testing.T) {
	c := ParseSRT("1\r\n00:01:02,125 --> 00:01:03,500\r\nПервый\r\nвторой\r\n\r\n2\r\n00:01:04,000 --> 00:01:05,000\r\nТретий\r\n")
	if len(c) != 2 || c[0].Start != 62.125 || c[0].End != 63.5 || c[0].Text != "Первый\nвторой" {
		t.Fatalf("cues=%+v", c)
	}
}

type credRepo struct{ c domain.GoogleCredential }

func (r credRepo) Get(context.Context, string) (domain.GoogleCredential, error) { return r.c, nil }
func (r credRepo) Put(context.Context, domain.GoogleCredential) error           { return nil }
func (r credRepo) Delete(context.Context, string) error                         { return nil }

func statsGateway(t *testing.T, h http.HandlerFunc, cred domain.GoogleCredential) *Service {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	auth := googleauth.New(credRepo{cred}, "id", "secret", "")
	auth.YouTube = func(tok youtube.TokenSource) *youtube.Client {
		c := youtube.New(tok)
		c.APIURL = srv.URL
		return c
	}
	return &Service{Auth: auth}
}

func statsRequest(expected string) *workflow.ChannelStatsRequest {
	return &workflow.ChannelStatsRequest{AccountID: 7, CredentialRef: credentials.Ref("7"), ExpectedRemoteID: expected, MaxVideos: 5}
}

func TestChannelStatsStatuses(t *testing.T) {
	valid := domain.GoogleCredential{AccountID: "7", AccessToken: "tok", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}
	ok := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels":
			_, _ = w.Write([]byte(`{"items":[{"id":"UC1","snippet":{"title":"A"},"contentDetails":{"relatedPlaylists":{"uploads":"UU1"}},"statistics":{"viewCount":"9","subscriberCount":"3","videoCount":"1"}}]}`))
		case "/playlistItems":
			_, _ = w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"v"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"items":[{"id":"v","snippet":{"title":"T"},"statistics":{"viewCount":"4"}}]}`))
		}
	}
	got, err := statsGateway(t, ok, valid).ChannelStats(context.Background(), statsRequest("UC1"))
	if err != nil || got.Status != workflow.StatsOK || got.Channel.ChannelID != "UC1" || len(got.Videos) != 1 || got.QuotaUnits != 3 {
		t.Fatalf("ok: %v %+v", err, got)
	}
	if got, _ = statsGateway(t, ok, valid).ChannelStats(context.Background(), statsRequest("UC-other")); got.Status != workflow.StatsNeedsReauth || len(got.Videos) != 0 {
		t.Errorf("channel mismatch must need re-auth and return no data: %+v", got)
	}
	quota := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"quotaExceeded"}]}}`))
	}
	if got, _ = statsGateway(t, quota, valid).ChannelStats(context.Background(), statsRequest("")); got.Status != workflow.StatsQuota || got.QuotaUnits != 1 {
		t.Errorf("quota: %+v", got)
	}
	expired := valid
	expired.AccessToken, expired.RefreshToken = "", ""
	if got, _ = statsGateway(t, ok, expired).ChannelStats(context.Background(), statsRequest("")); got.Status != workflow.StatsNeedsReauth {
		t.Errorf("missing refresh token: %+v", got)
	}
	if _, err = statsGateway(t, ok, valid).ChannelStats(context.Background(), &workflow.ChannelStatsRequest{AccountID: 8, CredentialRef: credentials.Ref("7")}); err == nil {
		t.Error("credential of another account must be rejected")
	}
}
