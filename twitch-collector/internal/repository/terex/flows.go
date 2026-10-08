package terex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func (c *Client) CollectorConfig(ctx context.Context) (twitch.CollectorConfig, error) {
	var out twitch.CollectorConfig
	err := c.request(ctx, "GET", "/api/twitch/collector-config", nil, &out)
	if err == nil {
		err = out.Settings.Validate()
	}
	return out, err
}

func (c *Client) FlowCapacity(ctx context.Context, id string) (twitch.FlowCapacity, error) {
	var out twitch.FlowCapacity
	err := c.request(ctx, http.MethodGet, "/api/twitch/flows/"+url.PathEscape(id)+"/capacity", nil, &out)
	return out, err
}

func (c *Client) AbortImport(ctx context.Context, imp twitch.Import, reason string) error {
	return c.request(ctx, http.MethodPost, "/api/imports/"+url.PathEscape(imp.ImportID)+"/abort", map[string]any{
		"upload_generation": imp.UploadGeneration,
		"reason":            reason,
	}, nil)
}
func (c *Client) Observe(ctx context.Context, q twitch.ObservationRequest) ([]twitch.Import, error) {
	var out struct {
		Imports []twitch.Import `json:"imports"`
	}
	err := c.request(ctx, "POST", "/api/twitch/observations", q, &out)
	return out.Imports, err
}
func (c *Client) ReserveImport(ctx context.Context, t domain.Task) (twitch.Import, error) {
	title := strings.TrimSpace(t.Clip.Title)
	if title == "" {
		title = "Twitch clip " + t.Clip.ID
	}
	if r := []rune(title); len(r) > 100 {
		title = string(r[:100])
	}
	q := twitch.ImportRequest{Epoch: t.Epoch, SchemaVersion: 2, IdempotencyKey: "twitch:" + t.Clip.ID, FlowID: t.FlowID, FlowVersion: t.FlowVersion, ReservationID: t.Reservation.ReservationID, UploadGeneration: t.Reservation.UploadGeneration, Source: twitch.Source{
		Platform: "twitch", ClipID: t.Clip.ID, BroadcasterID: t.Clip.BroadcasterID, BroadcasterLogin: t.Clip.BroadcasterName, Title: title, URL: "https://clips.twitch.tv/" + t.Clip.ID, CreatedAt: t.Clip.CreatedAt, ObservedAt: time.Now().UTC(), ViewCount: t.Clip.ViewCount, DurationSec: t.Clip.Duration, VideoID: t.Clip.VideoID, VODOffset: t.Clip.VODOffset, GameID: t.Clip.GameID, Language: t.Clip.Language, ThumbnailURL: t.Clip.ThumbnailURL}}
	var out twitch.Import
	err := c.request(ctx, "POST", "/api/imports", q, &out)
	if err == nil && out.ImportID == "" {
		err = errors.New("Terex returned an empty import ID")
	}
	if err == nil && out.Rejected {
		err = &domain.Failure{Kind: "blocked", Message: "Source globally blocked by operator"}
	}
	return out, err
}
func (c *Client) UploadImport(ctx context.Context, imp twitch.Import, file domain.Download, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	u, err := url.Parse(imp.UploadURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return &domain.Failure{Kind: "import", Message: "Terex returned no valid upload reservation", Retryable: true}
	}
	if imp.UploadExpiresAt != nil && time.Until(*imp.UploadExpiresAt) < time.Second {
		return &domain.Failure{Kind: "import", Message: "Upload reservation expired; retry will reserve a new generation", Retryable: true}
	}
	f, err := os.Open(file.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, imp.UploadURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = file.Size
	req.Header.Set("Content-Type", "video/mp4")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &domain.Failure{Kind: "network", Message: "Source upload unavailable", Retryable: true}
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &domain.Failure{Kind: "upload", Message: fmt.Sprintf("Source upload failed (HTTP %d)", res.StatusCode), HTTPStatus: res.StatusCode, Retryable: res.StatusCode == 403 || res.StatusCode == 429 || res.StatusCode >= 500}
	}
	if err = guard(ctx); err != nil {
		return err
	}
	var completed twitch.Import
	return c.request(ctx, "POST", "/api/imports/"+url.PathEscape(imp.ImportID)+"/complete", twitch.ImportCompleteRequest{Epoch: imp.Epoch, SHA256: file.SHA256, SizeBytes: file.Size, UploadGeneration: imp.UploadGeneration}, &completed)
}
