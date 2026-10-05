package terex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

type Client struct {
	Base, Token string
	HTTP        *http.Client
}

func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, path string, in, out any) error {
	var data []byte
	var err error
	if in != nil {
		data, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &domain.Failure{Kind: "network", Message: "Terex request unavailable", Retryable: true}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &domain.Failure{Kind: "import", Message: fmt.Sprintf("Terex request failed (HTTP %d)", res.StatusCode), HTTPStatus: res.StatusCode, Retryable: res.StatusCode == 429 || res.StatusCode >= 500, RetryAfter: retryAfter(res)}
	}
	if out == nil {
		return nil
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out); err != nil {
		return errors.New("invalid Terex response")
	}
	return nil
}
func retryAfter(res *http.Response) time.Duration {
	if res.StatusCode == 429 {
		return 30 * time.Minute
	}
	return 0
}
func (c *Client) Settings(ctx context.Context) (domain.Settings, error) {
	var s twitch.Settings
	err := c.request(ctx, "GET", "/api/twitch/settings", nil, &s)
	if err == nil {
		err = s.Validate()
		if err != nil {
			err = &domain.Failure{Kind: "configuration", Message: err.Error()}
		}
	}
	return s, err
}
func (c *Client) PendingCount(ctx context.Context) (int, error) {
	var imports []twitch.Import
	err := c.request(ctx, "GET", "/api/imports?pending=true", nil, &imports)
	return len(imports), err
}
func (c *Client) Import(ctx context.Context, clip domain.Clip, file domain.Download, account int64, guard func(context.Context) error) (string, error) {
	if err := guard(ctx); err != nil {
		return "", err
	}
	title := strings.TrimSpace(clip.Title)
	if title == "" {
		title = "Twitch clip " + clip.ID
	}
	if runes := []rune(title); len(runes) > 100 {
		title = string(runes[:100])
	}
	input := twitch.ImportRequest{SchemaVersion: 1, IdempotencyKey: "twitch:" + clip.ID, AccountID: account, Source: twitch.Source{Platform: "twitch", ClipID: clip.ID, BroadcasterID: clip.BroadcasterID, BroadcasterLogin: clip.BroadcasterName, Title: title, URL: "https://clips.twitch.tv/" + clip.ID, CreatedAt: clip.CreatedAt, ObservedAt: time.Now().UTC(), ViewCount: clip.ViewCount, DurationSec: file.Duration, VideoID: clip.VideoID, VODOffset: clip.VODOffset}}
	var imp twitch.Import
	if err := c.request(ctx, "POST", "/api/imports", input, &imp); err != nil {
		return "", err
	}
	if imp.ImportID == "" {
		return "", errors.New("Terex returned an empty import ID")
	}
	if imp.Rejected {
		return "", &domain.Failure{Kind: "import", Message: "Terex import was rejected"}
	}
	if imp.ClipID != "" {
		return imp.ImportID, nil
	}
	if imp.UploadURL == "" {
		return "", errors.New("Terex returned no upload reservation")
	}
	u, err := url.Parse(imp.UploadURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("Terex returned an invalid upload URL")
	}
	if imp.UploadExpiresAt != nil && time.Until(*imp.UploadExpiresAt) < time.Second {
		return "", &domain.Failure{Kind: "network", Message: "upload reservation expired", Retryable: true}
	}
	if err = guard(ctx); err != nil {
		return "", err
	}
	f, err := os.Open(file.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, imp.UploadURL, f)
	if err != nil {
		return "", err
	}
	req.ContentLength = file.Size
	req.Header.Set("Content-Type", "video/mp4")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", &domain.Failure{Kind: "network", Message: "source upload unavailable", Retryable: true}
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", &domain.Failure{Kind: "upload", Message: fmt.Sprintf("source upload failed (HTTP %d)", res.StatusCode), HTTPStatus: res.StatusCode, Retryable: res.StatusCode == 429 || res.StatusCode >= 500, RetryAfter: retryAfter(res)}
	}
	if err = guard(ctx); err != nil {
		return "", err
	}
	var completed twitch.Import
	err = c.request(ctx, "POST", "/api/imports/"+url.PathEscape(imp.ImportID)+"/complete", twitch.ImportCompleteRequest{SHA256: file.SHA256, SizeBytes: file.Size}, &completed)
	if err != nil {
		return "", err
	}
	return imp.ImportID, nil
}
