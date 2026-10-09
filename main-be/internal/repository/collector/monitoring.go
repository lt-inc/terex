package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"terex/main-be/internal/domain"
	"time"
)

type Monitoring struct {
	URL, Token string
	client     http.Client
}

func NewMonitoring(base, token string) *Monitoring {
	return &Monitoring{URL: strings.TrimRight(base, "/"), Token: token, client: http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Monitoring) get(ctx context.Context, path string, out any) error {
	if c.URL == "" || c.Token == "" {
		return fmt.Errorf("collector monitoring is not configured")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/"+path, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("collector monitoring HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func (c *Monitoring) Status(ctx context.Context) (out domain.TwitchCollectorStatus, err error) {
	err = c.get(ctx, "status", &out)
	out.Available = err == nil
	return
}
func (c *Monitoring) Diagnostics(ctx context.Context, id string) (out domain.TwitchDiagnosticCollector, err error) {
	err = c.get(ctx, "flows/"+url.PathEscape(id)+"/diagnostics", &out)
	out.Available = err == nil
	return
}
