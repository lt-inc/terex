package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"terex/gpu-be/internal/domain"
)

func (c *Client) Probe(ctx context.Context, in string) (domain.MediaProbe, error) {
	b, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json", in).Output()
	if err != nil {
		return domain.MediaProbe{}, fmt.Errorf("ffprobe: %w", err)
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(b, &raw); err != nil {
		return domain.MediaProbe{}, err
	}
	p := domain.MediaProbe{}
	p.DurationSec, err = strconv.ParseFloat(raw.Format.Duration, 64)
	if err != nil {
		return p, err
	}
	for _, v := range raw.Streams {
		if v.Type == "video" && p.Width == 0 {
			p.Width, p.Height = v.Width, v.Height
		}
		if v.Type == "audio" {
			p.HasAudio = true
		}
	}
	return p, nil
}
func (c *Client) VerifyIntegrity(ctx context.Context, in string) error {
	args := append([]string{"-xerror"}, c.hwaccel()...)
	args = append(args, "-i", in, "-map", "0:v:0", "-map", "0:a?", "-f", "null", "-")
	return c.run(ctx, "integrity", args...)
}
