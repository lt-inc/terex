// Package jobs creates and manages video jobs and dispatches them to gpu-be.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/telemetry"
)

type Service struct {
	Jobs     domain.JobRepo
	Assets   domain.AssetRepo
	Accounts domain.AccountRepo
	Broker   domain.Broker
}

func shortID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseSchedule combines date ("2006-01-02") and time ("15:04") (UTC+7, as the FE shows) into an instant.
func ParseSchedule(sc domain.PublishSchedule) *time.Time {
	if sc.Date == "" || sc.Time == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", sc.Date+" "+sc.Time, time.FixedZone("UTC+7", 7*3600))
	if err != nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func step(sc domain.PublishSchedule) int {
	switch v := sc.Step.(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// subJob builds one sub-job from per-platform account names and schedules; nil when no accounts.
func (s *Service) subJob(ctx context.Context, jobID string, accounts map[string][]string, schedules map[string]domain.PublishSchedule, bannerName string, targets []domain.AccountTarget) (*domain.NewSubJob, error) {
	var ids []int64
	resolved := []domain.AccountTarget{}
	seen := map[int64]bool{}
	for _, target := range targets {
		if target.AccountID <= 0 || seen[target.AccountID] || target.IntervalSeconds < 0 {
			return nil, fmt.Errorf("%w: invalid account target", domain.ErrInvalid)
		}
		if target.Timezone == "" {
			target.Timezone = "Asia/Tomsk"
		}
		if _, err := time.LoadLocation(target.Timezone); err != nil {
			return nil, fmt.Errorf("%w: invalid timezone", domain.ErrInvalid)
		}
		if _, err := s.Accounts.GetAccount(ctx, target.AccountID); err != nil {
			return nil, err
		}
		ids = append(ids, target.AccountID)
		resolved = append(resolved, target)
		seen[target.AccountID] = true
	}
	for _, platform := range []string{"youtube", "tiktok", "instagram"} {
		for _, name := range accounts[platform] {
			id, err := s.Accounts.GetOrCreateAccount(ctx, platform, name)
			if err != nil {
				return nil, err
			}
			if !seen[id] && len(targets) == 0 {
				sc := schedules[platform]
				n := step(sc)
				if n < 0 {
					return nil, fmt.Errorf("%w: invalid publish interval", domain.ErrInvalid)
				}
				if str, ok := sc.Step.(string); ok && strings.TrimSpace(str) != "" {
					if _, err := strconv.Atoi(strings.TrimSpace(str)); err != nil {
						return nil, fmt.Errorf("%w: use numeric account interval_seconds", domain.ErrInvalid)
					}
				}
				ids = append(ids, id)
				resolved = append(resolved, domain.AccountTarget{AccountID: id, StartAt: ParseSchedule(sc), IntervalSeconds: n * 60, Timezone: "Asia/Tomsk"})
				seen[id] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sj := &domain.NewSubJob{SubJobID: jobID + "-" + shortID(), AccountIDs: ids, Targets: resolved}
	for _, platform := range []string{"youtube", "tiktok", "instagram"} { // first platform with a schedule wins
		if sc, ok := schedules[platform]; ok {
			if dt := ParseSchedule(sc); dt != nil {
				sj.PublishDatetime, sj.PublishStep = dt, step(sc)
				break
			}
			if st := step(sc); st > 0 && sj.PublishStep == 0 {
				sj.PublishStep = st
			}
		}
	}
	if bannerName != "" {
		bid, err := s.Assets.GetOrCreateAssetByName(ctx, domain.KindBanner, bannerName)
		if err != nil {
			return nil, err
		}
		sj.BannerID = &bid
	}
	return sj, nil
}

// FlowJob marks a job created by an automatic flow; the zero value is an ordinary job.
type FlowJob struct{ SourceKind, SourceID, OutputFormat string }

// Create stores a job from the FE card (or a channel template) and queues it on the GPU.
func (s *Service) Create(ctx context.Context, card domain.VideoCard, channelID *int64) (string, error) {
	return s.create(ctx, card, channelID, FlowJob{})
}

// CreateFlowJob is Create for jobs that belong to an automatic flow (see domain.YTJobSourceKind).
func (s *Service) CreateFlowJob(ctx context.Context, card domain.VideoCard, fj FlowJob) (string, error) {
	return s.create(ctx, card, nil, fj)
}

func (s *Service) create(ctx context.Context, card domain.VideoCard, channelID *int64, fj FlowJob) (_ string, err error) {
	jobID := shortID()
	ctx, span := telemetry.Start(ctx, "job.create", attribute.String("job.id", jobID), attribute.String("youtube.url", card.YoutubeURL))
	defer func() { telemetry.End(span, err) }()
	mode := card.SliceMode
	if mode != string(msg.SliceHighlights) && mode != string(msg.SliceWhole) {
		mode = string(msg.SliceFull)
	}
	approve := card.ApproveMode
	if approve != domain.ApproveAuto {
		approve = domain.ApproveBy
	}
	minDur := card.MinVideoDuration
	if minDur <= 0 {
		minDur = 60
	}
	watermark, wmErr := card.Watermark.Normalize()
	if wmErr != nil {
		return "", fmt.Errorf("%w: %v", domain.ErrInvalid, wmErr)
	}
	nj := domain.NewJob{
		JobID: jobID, ChannelID: channelID, YoutubeURL: card.YoutubeURL, IsVertical: card.MakeVertical,
		IsSubtitles: card.AddSubtitles, SliceMode: mode, MinVideoDuration: minDur,
		IsCircle: card.Shape == "circle", ApproveMode: approve, Trace: telemetry.TraceID(ctx),
		SourceKind: fj.SourceKind, SourceID: fj.SourceID, OutputFormat: fj.OutputFormat, Brand: card.Brand, Watermark: watermark,
	}
	if card.AddBackground && card.BackgroundName != "" {
		id, err := s.Assets.GetOrCreateAssetByName(ctx, domain.KindBackground, card.BackgroundName)
		if err != nil {
			return "", err
		}
		nj.BackgroundID = &id
	}

	if len(card.Banners) == 0 {
		sj, err := s.subJob(ctx, jobID, map[string][]string{
			"youtube": card.YoutubeAccounts, "tiktok": card.TiktokAccounts, "instagram": card.InstagramAccounts,
		}, card.Schedules, "", card.Targets)
		if err != nil {
			return "", err
		}
		if sj != nil {
			nj.SubJobs = append(nj.SubJobs, *sj)
		}
	}
	for _, b := range card.Banners {
		sj, err := s.subJob(ctx, jobID, map[string][]string{
			"youtube": b.YoutubeAccounts, "tiktok": b.TiktokAccounts, "instagram": b.InstagramAccounts,
		}, b.Schedules, b.BannerName, b.Targets)
		if err != nil {
			return "", err
		}
		if sj != nil {
			nj.SubJobs = append(nj.SubJobs, *sj)
		}
	}

	if err = s.Jobs.CreateJob(ctx, nj); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "job created", "job_id", jobID, "url", card.YoutubeURL, "slice_mode", mode,
		"approve_mode", approve, "sub_jobs", len(nj.SubJobs), "channel_id", channelID)
	return jobID, s.Dispatch(ctx, jobID)
}

// Dispatch sends the job to gpu-be.
func (s *Service) Dispatch(ctx context.Context, jobID string) error {
	m, err := s.Jobs.JobProcessMessage(ctx, jobID)
	if err != nil {
		return err
	}
	if err := s.Broker.Publish(ctx, msg.QueueProcessVideo, m); err != nil {
		slog.ErrorContext(ctx, "job dispatch failed", "job_id", jobID, "err", err)
		_ = s.Jobs.SetJobStatus(ctx, jobID, "failed", "queue")
		return fmt.Errorf("queue job: %w", err)
	}
	return s.Jobs.SetJobStatus(ctx, jobID, "queued", "")
}

func (s *Service) List(ctx context.Context, showArchived bool) ([]domain.Job, error) {
	return s.Jobs.ListJobs(ctx, showArchived)
}

func (s *Service) Update(ctx context.Context, id string, u domain.JobUpdate) (domain.Job, error) {
	if err := s.Jobs.UpdateJob(ctx, id, u); err != nil {
		return domain.Job{}, err
	}
	return s.Jobs.GetJob(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.Jobs.DeleteJob(ctx, id) }

func (s *Service) UpdateSubJob(ctx context.Context, id string, u domain.SubJobUpdate) (domain.SubJob, error) {
	var accountIDs []int64
	if u.AccountNames != nil {
		accountIDs = []int64{}
		// platform is not part of the update payload: match existing accounts by name
		all, err := s.Accounts.ListAccounts(ctx)
		if err != nil {
			return domain.SubJob{}, err
		}
		for _, n := range u.AccountNames {
			for _, a := range all {
				if a.Name == n {
					accountIDs = append(accountIDs, a.ID)
				}
			}
		}
	}
	var bannerID *int64
	if u.BannerName != nil && *u.BannerName != "" {
		bid, err := s.Assets.GetOrCreateAssetByName(ctx, domain.KindBanner, *u.BannerName)
		if err != nil {
			return domain.SubJob{}, err
		}
		bannerID = &bid
	}
	if err := s.Jobs.UpdateSubJob(ctx, id, u, accountIDs, bannerID); err != nil {
		return domain.SubJob{}, err
	}
	// reload through the parent job
	jobs, err := s.Jobs.ListJobs(ctx, true)
	if err != nil {
		return domain.SubJob{}, err
	}
	for _, j := range jobs {
		for _, sj := range j.SubJobs {
			if sj.SubJobID == id {
				return sj, nil
			}
		}
	}
	return domain.SubJob{}, domain.ErrNotFound
}

func (s *Service) DeleteSubJob(ctx context.Context, id string) error {
	return s.Jobs.DeleteSubJob(ctx, id)
}

// HandleStatus applies a stage status event from gpu-be.
func (s *Service) HandleStatus(ctx context.Context, st msg.JobStatus) error {
	attrs := []any{"job_id", st.JobID, "stage", st.Stage, "status", st.Status}
	if st.FailPlace != "" {
		attrs = append(attrs, "fail_place", st.FailPlace)
	}
	slog.InfoContext(ctx, "job status from gpu", attrs...)
	switch st.Status {
	case "running", "queued":
		return s.Jobs.SetJobStatus(ctx, st.JobID, "processing", "")
	case "failed":
		return s.Jobs.SetJobStatus(ctx, st.JobID, "failed", st.FailPlace)
	}
	return nil // "done" per stage; the job is done on clips_ready
}
