// Package watermark prepares the manual blur editor: still frames of a video (the newest of a channel,
// or a given one) to draw regions on, plus the regions the automatic detector suggests.
package watermark

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/workflow"
)

// Frames is satisfied by the gpu-be workflow gateway client.
type Frames interface {
	WatermarkFrames(context.Context, *workflow.WatermarkFramesRequest) (*workflow.WatermarkFramesResponse, error)
}

type Service struct {
	Feed   domain.Feed
	Frames Frames
}

// Frame is one still as a data URL, ready for an <img>.
type Frame struct {
	TimeSec float64 `json:"time_sec"`
	Image   string  `json:"image"`
}

type Result struct {
	VideoURL   string                `json:"video_url"`
	VideoTitle string                `json:"video_title,omitempty"`
	Width      int                   `json:"width"`
	Height     int                   `json:"height"`
	Frames     []Frame               `json:"frames"`
	Suggested  []msg.WatermarkRegion `json:"suggested"`
}

// Preview returns the editor material for link: a video link as it is, a channel link by way of the
// newest video of its feed.
func (s *Service) Preview(ctx context.Context, link string) (Result, error) {
	link = strings.TrimSpace(link)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !youtubeHost(u.Hostname()) {
		return Result{}, fmt.Errorf("%w: expected a YouTube channel or video link", domain.ErrInvalid)
	}
	res := Result{VideoURL: link}
	if !isVideo(u) {
		id, err := s.Feed.ResolveChannel(ctx, link)
		if err != nil {
			return Result{}, err
		}
		videos, err := s.Feed.Latest(ctx, id)
		if err != nil {
			return Result{}, err
		}
		if len(videos) == 0 {
			return Result{}, fmt.Errorf("%w: the channel has no videos to take frames from", domain.ErrInvalid)
		}
		res.VideoURL, res.VideoTitle = videos[0].URL, videos[0].Title
	}
	out, err := s.Frames.WatermarkFrames(ctx, &workflow.WatermarkFramesRequest{URL: res.VideoURL})
	if err != nil {
		return Result{}, err
	}
	res.Width, res.Height = out.Width, out.Height
	res.Suggested = out.Suggested
	if res.Suggested == nil {
		res.Suggested = []msg.WatermarkRegion{}
	}
	res.Frames = make([]Frame, 0, len(out.Frames))
	for _, f := range out.Frames {
		res.Frames = append(res.Frames, Frame{TimeSec: f.TimeSec, Image: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(f.JPEG)})
	}
	return res, nil
}

func youtubeHost(h string) bool {
	h = strings.ToLower(h)
	return h == "youtu.be" || h == "youtube.com" || strings.HasSuffix(h, ".youtube.com")
}

// isVideo tells a video link (watch, shorts, live, embed, youtu.be/<id>) from a channel link (@handle,
// /channel/, /c/, /user/).
func isVideo(u *url.URL) bool {
	if strings.EqualFold(u.Hostname(), "youtu.be") {
		return strings.Trim(u.Path, "/") != ""
	}
	if strings.HasPrefix(u.Path, "/watch") {
		return u.Query().Get("v") != ""
	}
	for _, p := range []string{"/shorts/", "/live/", "/embed/"} {
		if strings.HasPrefix(u.Path, p) && len(u.Path) > len(p) {
			return true
		}
	}
	return false
}
