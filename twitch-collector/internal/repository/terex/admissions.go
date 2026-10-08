package terex

import (
	"context"
	"net/http"
	"terex/proto/twitch"
)

func (c *Client) Admissions(ctx context.Context, q twitch.AdmissionRequest) (twitch.AdmissionResult, error) {
	var out twitch.AdmissionResult
	err := c.request(ctx, http.MethodPost, "/api/twitch/collector/admissions", q, &out)
	return out, err
}
