package terex

import (
	"context"
	"net/http"
	"terex/proto/twitch"
)

func (c *Client) AcknowledgeReset(ctx context.Context, q twitch.ResetCollectorAck) error {
	return c.request(ctx, http.MethodPost, "/api/twitch/reset/collector-ack", q, nil)
}
