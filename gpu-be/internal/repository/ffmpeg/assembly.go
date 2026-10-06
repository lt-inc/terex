package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"terex/gpu-be/internal/domain"
)

// Bound assembly bitrate as well as scratch bytes, retaining 1080p30/CQ20.
func (c *Client) assemblyVcodec() []string {
	return append(c.vcodec(20, "veryfast"), "-maxrate", "12M", "-bufsize", "24M")
}

func (c *Client) NormalizeAssemblySegment(ctx context.Context, s domain.AssemblySegment, out string) error {
	if s.StartSec < 0 || s.EndSec <= s.StartSec || math.IsNaN(s.StartSec) || math.IsNaN(s.EndSec) || math.IsInf(s.StartSec, 0) || math.IsInf(s.EndSec, 0) {
		return fmt.Errorf("invalid segment bounds")
	}
	p, err := c.Probe(ctx, s.File)
	if err != nil {
		return err
	}
	if s.EndSec > p.DurationSec+0.05 {
		return fmt.Errorf("segment exceeds source duration")
	}
	frames := int(math.Round((s.EndSec - s.StartSec) * 30))
	if frames < 1 {
		return fmt.Errorf("empty segment")
	}
	args := append(c.hwaccel(), "-i", s.File)
	audio := "0:a:0"
	if !p.HasAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
		audio = "1:a:0"
	}
	args = append(args, "-ss", strconv.FormatFloat(s.StartSec, 'f', 6, 64), "-t", strconv.FormatFloat(float64(frames)/30, 'f', 6, 64), "-map", "0:v:0", "-map", audio,
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p",
		"-af", "loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000,apad", "-ac", "2", "-ar", "48000")
	args = append(args, c.assemblyVcodec()...)
	args = append(args, "-c:a", "aac", "-b:a", "192k", "-video_track_timescale", "15360", "-movflags", "+faststart", out)
	return c.run(ctx, "assembly_segment", args...)
}

func (c *Client) AssemblyTransition(ctx context.Context, channel, out string) error {
	name := strings.TrimSpace(channel)
	if name == "" || len(name) > 128 {
		return fmt.Errorf("invalid transition channel")
	}
	textFile := out + ".txt"
	if err := os.WriteFile(textFile, []byte(name), 0o600); err != nil {
		return err
	}
	defer os.Remove(textFile)
	escaped := strings.NewReplacer(":", `\\:`, "'", `\\'`, ",", `\\,`).Replace(filepath.ToSlash(textFile))
	filter := "drawtext=font='Liberation Sans':textfile='" + escaped + "':expansion=none:fontcolor=white:fontsize=72:x=(w-text_w)/2:y=(h-text_h)/2"
	args := []string{"-f", "lavfi", "-i", "color=c=0x151521:s=1920x1080:r=30", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "1", "-vf", filter, "-map", "0:v:0", "-map", "1:a:0"}
	args = append(args, c.assemblyVcodec()...)
	args = append(args, "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-video_track_timescale", "15360", out)
	return c.run(ctx, "assembly_card", args...)
}

func (c *Client) ConcatAssembly(ctx context.Context, files []string, out string) error {
	if len(files) < 1 || len(files) > 119 {
		return fmt.Errorf("invalid assembly timeline")
	}
	var list strings.Builder
	for _, file := range files {
		list.WriteString("file '")
		list.WriteString(strings.ReplaceAll(filepath.ToSlash(file), "'", "'\\''"))
		list.WriteString("'\n")
	}
	listFile := out + ".concat.txt"
	if err := os.WriteFile(listFile, []byte(list.String()), 0o600); err != nil {
		return err
	}
	defer os.Remove(listFile)
	// Re-encoding eliminates AAC priming and timestamp drift between clips/cards.
	args := append(c.hwaccel(), "-f", "concat", "-safe", "0", "-i", listFile)
	args = append(args, c.assemblyVcodec()...)
	args = append(args, "-r", "30", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", out)
	return c.run(ctx, "assembly_concat", args...)
}

// Compatibility path for callers that already hold local sources. Production
// uses the methods above to normalize then release each downloaded source.
func (c *Client) Assemble(ctx context.Context, segments []domain.AssemblySegment, out string, bumperFrames int) error {
	if len(segments) < 3 || len(segments) > 60 || bumperFrames != 30 {
		return fmt.Errorf("invalid assembly size or transition")
	}
	dir, err := os.MkdirTemp(filepath.Dir(out), "segments-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	files := make([]string, 0, 2*len(segments)-1)
	for i, segment := range segments {
		target := filepath.Join(dir, fmt.Sprintf("clip-%02d.mp4", i))
		if err = c.NormalizeAssemblySegment(ctx, segment, target); err != nil {
			return err
		}
		files = append(files, target)
		if i < len(segments)-1 {
			card := filepath.Join(dir, fmt.Sprintf("card-%02d.mp4", i))
			if err = c.AssemblyTransition(ctx, segments[i+1].NextChannel, card); err != nil {
				return err
			}
			files = append(files, card)
		}
	}
	return c.ConcatAssembly(ctx, files, out)
}
