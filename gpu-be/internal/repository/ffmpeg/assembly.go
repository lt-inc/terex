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

// Assemble normalizes each segment independently, including clips without an
// audio track. Every segment and card has the same time base and stream layout.
func (c *Client) Assemble(ctx context.Context, segments []domain.AssemblySegment, out string, bumperFrames int) error {
	if len(segments) < 3 || len(segments) > 10 || bumperFrames != 30 {
		return fmt.Errorf("invalid assembly size or transition")
	}
	dir, err := os.MkdirTemp(filepath.Dir(out), "segments-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var files []string
	for i, s := range segments {
		if s.StartSec < 0 || s.EndSec <= s.StartSec || math.IsNaN(s.StartSec) || math.IsNaN(s.EndSec) || math.IsInf(s.EndSec, 0) {
			return fmt.Errorf("invalid segment bounds")
		}
		p, e := c.Probe(ctx, s.File)
		if e != nil {
			return e
		}
		if s.EndSec > p.DurationSec+0.05 {
			return fmt.Errorf("segment exceeds source duration")
		}
		frames := int(math.Round((s.EndSec - s.StartSec) * 30))
		if frames < 1 {
			return fmt.Errorf("empty segment")
		}
		target := filepath.Join(dir, fmt.Sprintf("clip-%02d.mp4", i))
		args := []string{"-i", s.File}
		audio := "0:a:0"
		if !p.HasAudio {
			args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
			audio = "1:a:0"
		}
		args = append(args, "-ss", strconv.FormatFloat(s.StartSec, 'f', 6, 64), "-t", strconv.FormatFloat(float64(frames)/30, 'f', 6, 64), "-map", "0:v:0", "-map", audio,
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p",
			"-af", "loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000,apad", "-ac", "2", "-ar", "48000")
		args = append(args, c.vcodec(20, "veryfast")...)
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-video_track_timescale", "15360", "-movflags", "+faststart", target)
		if err = c.run(ctx, "assembly_segment", args...); err != nil {
			return err
		}
		files = append(files, target)
		if i < len(segments)-1 {
			card := filepath.Join(dir, fmt.Sprintf("card-%02d.mp4", i))
			textFile := filepath.Join(dir, fmt.Sprintf("channel-%02d.txt", i))
			name := strings.TrimSpace(segments[i+1].NextChannel)
			if name == "" || len(name) > 128 {
				return fmt.Errorf("invalid transition channel")
			}
			if err = os.WriteFile(textFile, []byte(name), 0o600); err != nil {
				return err
			}
			escaped := strings.NewReplacer(`\`, `/`, ":", `\:`, "'", `\'`, ",", `\,`).Replace(textFile)
			filter := "drawtext=font='Liberation Sans':textfile='" + escaped + "':expansion=none:fontcolor=white:fontsize=72:x=(w-text_w)/2:y=(h-text_h)/2"
			args = []string{"-f", "lavfi", "-i", "color=c=0x151521:s=1920x1080:r=30", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "1", "-vf", filter, "-map", "0:v:0", "-map", "1:a:0"}
			args = append(args, c.vcodec(20, "veryfast")...)
			args = append(args, "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-video_track_timescale", "15360", card)
			if err = c.run(ctx, "assembly_card", args...); err != nil {
				return err
			}
			files = append(files, card)
		}
	}
	// Files are generated locally; no caller-supplied names enter concat syntax.
	var list strings.Builder
	for _, file := range files {
		list.WriteString("file '")
		list.WriteString(strings.ReplaceAll(filepath.ToSlash(file), "'", "'\\''"))
		list.WriteString("'\n")
	}
	listFile := filepath.Join(dir, "concat.txt")
	if err = os.WriteFile(listFile, []byte(list.String()), 0o600); err != nil {
		return err
	}
	// Re-encoding the final timeline eliminates AAC priming and concat timestamp
	// drift across independently encoded clips/cards.
	args := []string{"-f", "concat", "-safe", "0", "-i", listFile}
	args = append(args, c.vcodec(20, "veryfast")...)
	args = append(args, "-r", "30", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", out)
	return c.run(ctx, "assembly_concat", args...)
}
