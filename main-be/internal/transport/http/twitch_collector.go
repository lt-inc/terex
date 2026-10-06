package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func (p *CollectorProxy) call(ctx context.Context, method, path string, body, out any) error {
	if p == nil || p.URL == "" || p.Token == "" {
		return fmt.Errorf("%w: коллектор не настроен", domain.ErrConflict)
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.URL, "/")+"/api/"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: коллектор недоступен", domain.ErrConflict)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&msg)
		if msg.Error == "" {
			msg.Error = "запрос коллектора не выполнен"
		}
		kind := domain.ErrConflict
		if res.StatusCode == 400 {
			kind = domain.ErrInvalid
		}
		return fmt.Errorf("%w: %s", kind, msg.Error)
	}
	if out == nil {
		return nil
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: неверный ответ коллектора", domain.ErrConflict)
	}
	return nil
}
func (a *API) resolveFlowChannels(ctx context.Context, f *twitch.Flow, create bool) error {
	if f.SourceMode != "channels" || len(f.ChannelInputs) == 0 {
		return nil
	}
	if len(f.ChannelInputs) > 200 {
		return domain.ErrInvalid
	}
	if !create {
		old, err := a.Twitch.Flow(ctx, f.ID)
		if err != nil {
			return err
		}
		if strings.Join(old.ChannelInputs, "\n") == strings.Join(f.ChannelInputs, "\n") {
			f.BroadcasterIDs = old.BroadcasterIDs
			return nil
		}
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, input := range f.ChannelInputs {
		var channel struct {
			BroadcasterID string `json:"broadcaster_id"`
		}
		if err := a.Collector.call(ctx, http.MethodPost, "channels", map[string]string{"input": input}, &channel); err != nil {
			return err
		}
		if channel.BroadcasterID == "" {
			return domain.ErrInvalid
		}
		if !seen[channel.BroadcasterID] {
			ids = append(ids, channel.BroadcasterID)
			seen[channel.BroadcasterID] = true
		}
	}
	f.BroadcasterIDs = ids
	return nil
}
func (a *API) collectorFilterSources(r *http.Request, flow, state string) ([]string, error) {
	switch state {
	case "failed", "queued", "downloading", "importing", "verifying", "retry_wait", "waiting_quota":
	default:
		return nil, nil
	}
	if a.Collector == nil || a.Collector.URL == "" {
		return nil, nil
	}
	var data struct {
		Items []struct {
			ClipID string `json:"clip_id"`
		} `json:"items"`
	}
	if err := a.Collector.call(r.Context(), http.MethodGet, "flows/"+url.PathEscape(flow)+"/clip-status?state="+url.QueryEscape(state), nil, &data); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(data.Items))
	for _, v := range data.Items {
		out = append(out, v.ClipID)
	}
	return out, nil
}
