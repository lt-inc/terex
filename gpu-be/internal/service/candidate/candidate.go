// Package candidate: transcribe audio, build subtitles and pick clip intervals.
package candidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/reporter"
	"terex/proto/telemetry"
)

const stage = "candidate"

type Service struct {
	Store  domain.ObjectStore
	STT    domain.Transcriber
	LLM    domain.HighlightFinder
	Audio  domain.AudioAnalyzer // optional: energy/laughter hints for the LLM
	Limits domain.ClipLimits    // highlights mode: the LLM picks the length inside these bounds
	// PromptTags lists the hints the LLM is allowed to see (energy:high, laugh, fast, pause_before).
	PromptTags []string
	Broker     domain.Broker
	Rep        *reporter.Reporter
	TmpDir     string
}

func (s *Service) Handle(ctx context.Context, msg domain.ProcessVideo) (err error) {
	ctx, span := telemetry.Start(ctx, stage+" handle", attribute.String("job.id", msg.JobID))
	defer func() { telemetry.End(span, err) }()
	s.Rep.Report(ctx, msg.JobID, stage, "running", "")
	defer func() {
		if err != nil {
			s.Rep.Report(ctx, msg.JobID, stage, "failed", stage)
		}
	}()

	if err = os.MkdirAll(s.TmpDir, 0o755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.TmpDir, msg.JobID+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	audio := filepath.Join(dir, msg.JobID+".m4a")
	if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketProxy, Object: msg.JobID + ".m4a"}, audio); err != nil {
		return err
	}
	segs, err := s.STT.Transcribe(ctx, audio)
	if err != nil {
		return err
	}

	var intervals []domain.Interval
	if msg.Options.SliceMode == domain.SliceHighlights {
		if intervals, err = s.highlights(ctx, msg.JobID, audio, segs); err != nil {
			return err
		}
	} else {
		minDur := msg.Options.MinDuration
		if minDur <= 0 {
			minDur = 60
		}
		intervals = GroupSegments(segs, float64(minDur))
	}

	put := func(name string, data []byte) error {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		return s.Store.Upload(ctx, domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: msg.JobID + "/" + name}, p)
	}
	if err = put("subs.srt", []byte(SRT(segs))); err != nil {
		return err
	}
	if err = put("subs.ass", []byte(ASS(segs, 4))); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(intervals, "", "  ")
	if err = put("intervals.json", raw); err != nil {
		return err
	}

	s.Rep.Report(ctx, msg.JobID, stage, "done", "")
	return s.Broker.Publish(ctx, domain.QueueRender, msg)
}

// highlights: sentences -> acoustic hints -> LLM picks (by sentence id) -> validated intervals.
func (s *Service) highlights(ctx context.Context, jobID, audioPath string, segs []domain.Segment) ([]domain.Interval, error) {
	sents := BuildSentences(segs)
	if len(sents) == 0 {
		return nil, errors.New("no speech found in the video")
	}
	s.annotate(ctx, audioPath, sents)

	picks, err := s.LLM.Find(ctx, FilterTags(sents, s.PromptTags), s.Limits)
	if err != nil {
		return nil, err
	}
	intervals, dropped := ResolvePicks(sents, picks, s.Limits)
	slog.InfoContext(ctx, "highlights resolved", "job_id", jobID, "sentences", len(sents), "picks", len(picks), "clips", len(intervals), "dropped", dropped)
	if len(intervals) == 0 {
		return nil, fmt.Errorf("no valid highlights (LLM returned %d picks, all rejected)", len(picks))
	}
	return intervals, nil
}

// annotate adds energy/laughter/rate/pause hints. Audio analysis is optional: a failure is logged
// and the LLM simply gets fewer hints.
func (s *Service) annotate(ctx context.Context, audioPath string, sents []domain.Sentence) {
	var energy domain.EnergySeries
	var events []domain.AudioEvent
	if s.Audio != nil {
		var err error
		if energy, err = s.Audio.Energy(ctx, audioPath); err != nil {
			slog.WarnContext(ctx, "energy analysis failed; continuing without it", "err", err)
		}
		if events, err = s.Audio.Events(ctx, audioPath); err != nil {
			slog.WarnContext(ctx, "audio events (laugh/shout/gasp/music) unavailable; continuing without them", "err", err)
		}
	}
	Annotate(sents, energy, events)

	counts := map[string]int{}
	for _, st := range sents {
		for _, t := range st.Tags {
			counts[t]++
		}
	}
	slog.InfoContext(ctx, "sentence hints computed", "sentences", len(sents), "energy_high", counts[TagEnergyHigh],
		"energy_spike", counts[TagEnergySpike], "laugh", counts[TagLaugh], "shout", counts[TagShout], "gasp", counts[TagGasp],
		"music", counts[TagMusic], "fast", counts[TagFast], "pause_before", counts[TagPauseBefore])
}
