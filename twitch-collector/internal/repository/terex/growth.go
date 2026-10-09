package terex

import (
	"context"
	"net/http"
	"net/url"
	"terex/proto/twitch"
)

func (c *Client) ObservationCandidates(ctx context.Context, id string) ([]twitch.Source, error) {
	out := []twitch.Source{}
	err := c.request(ctx, http.MethodGet, "/api/twitch/flows/"+url.PathEscape(id)+"/observation-candidates", nil, &out)
	return out, err
}
