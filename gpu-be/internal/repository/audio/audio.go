// Package audio extracts acoustic hints: loudness (RMS via ffmpeg PCM) and audio events (YAMNet).
package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/proto/telemetry"
)

const (
	sampleRate = 16000
	// EnergyStep is the RMS window length in seconds.
	EnergyStep = 0.4
	silenceDB  = -90.0
)

type Analyzer struct {
	// Cmd runs the YAMNet script, e.g. "python3 /opt/terex/yamnet.py". Empty disables audio events.
	Cmd string
}

func New(cmd string) *Analyzer { return &Analyzer{Cmd: cmd} }

// rmsDB returns the loudness of samples in dBFS (silenceDB for silence).
func rmsDB(samples []int16) float64 {
	if len(samples) == 0 {
		return silenceDB
	}
	var sum float64
	for _, v := range samples {
		f := float64(v) / 32768.0
		sum += f * f
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms < 1e-5 {
		return silenceDB
	}
	return 20 * math.Log10(rms)
}

// Energy decodes the audio to 16 kHz mono PCM and computes RMS per 0.4 s window, streaming (O(1) memory).
func (a *Analyzer) Energy(ctx context.Context, audioPath string) (series domain.EnergySeries, err error) {
	ctx, span := telemetry.Start(ctx, "audio energy")
	defer func() { telemetry.End(span, err) }()
	start := time.Now()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", audioPath, "-ac", "1", "-ar", fmt.Sprint(sampleRate), "-f", "s16le", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return series, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return series, err
	}

	series.Step = EnergyStep
	win := int(EnergyStep * sampleRate)
	buf := make([]byte, win*2)
	samples := make([]int16, win)
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		n, rerr := io.ReadFull(r, buf)
		if n >= 2 {
			cnt := n / 2
			for i := 0; i < cnt; i++ {
				samples[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
			}
			series.DB = append(series.DB, rmsDB(samples[:cnt]))
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				break
			}
			_ = cmd.Wait()
			return series, rerr
		}
	}
	if err = cmd.Wait(); err != nil {
		return series, fmt.Errorf("ffmpeg pcm: %w: %s", err, stderr.String())
	}
	span.SetAttributes(attribute.Int("energy.windows", len(series.DB)))
	slog.InfoContext(ctx, "audio energy computed", "windows", len(series.DB), "took", time.Since(start).Round(time.Millisecond))
	return series, nil
}

// Events runs the YAMNet script and returns per-window audio-event probabilities.
func (a *Analyzer) Events(ctx context.Context, audioPath string) (out []domain.AudioEvent, err error) {
	if strings.TrimSpace(a.Cmd) == "" {
		return nil, errors.New("audio events disabled (YAMNET_CMD is empty)")
	}
	ctx, span := telemetry.Start(ctx, "yamnet events")
	defer func() { telemetry.End(span, err) }()
	start := time.Now()

	parts := strings.Fields(a.Cmd)
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], audioPath)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yamnet script: %w: %s", err, tail(stderr.String()))
	}
	var res struct {
		Window  float64 `json:"window"`
		Windows []struct {
			T        float64 `json:"t"`
			Laugh    float64 `json:"laugh"`
			Applause float64 `json:"applause"`
			Shout    float64 `json:"shout"`
			Gasp     float64 `json:"gasp"`
			Music    float64 `json:"music"`
			Silence  float64 `json:"silence"`
		} `json:"windows"`
	}
	if err = json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse yamnet json: %w", err)
	}
	if res.Window <= 0 {
		res.Window = 0.975
	}
	for _, w := range res.Windows {
		out = append(out, domain.AudioEvent{
			Start: w.T, End: w.T + res.Window, Laugh: math.Max(w.Laugh, w.Applause),
			Shout: w.Shout, Gasp: w.Gasp, Music: w.Music, Silence: w.Silence,
		})
	}
	span.SetAttributes(attribute.Int("yamnet.windows", len(out)))
	slog.InfoContext(ctx, "audio events detected", "windows", len(out), "took", time.Since(start).Round(time.Millisecond))
	return out, nil
}

func tail(s string) string {
	if len(s) > 1500 {
		return s[len(s)-1500:]
	}
	return s
}
