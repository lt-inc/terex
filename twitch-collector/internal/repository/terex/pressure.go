package terex

import (
	"context"
	"net/http"
	"terex/proto/twitch"
)

func (c *Client) Pressure(ctx context.Context, q twitch.PressureRequest) (twitch.PressureResult, error) {
	var out twitch.PressureResult
	err := c.request(ctx, http.MethodPost, "/api/twitch/collector/pressure", q, &out)
	return out, err
}
