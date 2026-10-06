// Package publish is the publishing manager: stores clips from gpu-be, handles approval
// (by-approve / auto-approve), schedules publications and sends due ones to the GPU publisher.
package publish

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
)

type Manager struct {
	Clips    domain.ClipRepo
	Jobs     domain.JobRepo
	Broker   domain.Broker
	Gpu      domain.Gpu
	Now      func() time.Time
	Workflow interface {
		InitializeClip(context.Context, string, json.RawMessage) error
		Approve(context.Context, string) error
		AutoApprove(context.Context, string) error
		Reject(context.Context, string) error
		Tick(context.Context) error
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// HandleClipsReady stores clips, marks the job done and auto-approves when the job asks for it.
func (m *Manager) HandleClipsReady(ctx context.Context, ready msg.ClipsReady) error {
	slog.InfoContext(ctx, "clips ready", "job_id", ready.JobID, "clips", len(ready.Clips))
	idxByBanner := map[string]int{}
	clips := make([]domain.Clip, 0, len(ready.Clips))
	for _, c := range ready.Clips {
		clips = append(clips, domain.Clip{
			ClipID: c.ClipID, JobID: ready.JobID, BannerObject: c.Banner, Bucket: c.Object.Bucket, Object: c.Object.Object,
			Idx: idxByBanner[c.Banner], StartSec: c.Start, EndSec: c.End, Title: c.Title, Hashtags: c.Hashtags,
		})
		idxByBanner[c.Banner]++
	}
	if err := m.Clips.SaveClips(ctx, ready.JobID, clips); err != nil {
		return err
	}
	if m.Workflow != nil {
		for _, c := range ready.Clips {
			if err := m.Workflow.InitializeClip(ctx, c.ClipID, c.SourceManifest); err != nil {
				return err
			}
		}
	}
	if err := m.Jobs.SetJobStatus(ctx, ready.JobID, "done", ""); err != nil {
		return err
	}
	mode, err := m.Clips.JobApproveMode(ctx, ready.JobID)
	if err != nil {
		return err
	}
	if mode == domain.ApproveAuto {
		for _, c := range clips {
			approve := m.Approve
			if m.Workflow != nil {
				approve = m.Workflow.AutoApprove
			}
			if err := approve(ctx, c.ClipID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) Approve(ctx context.Context, clipID string) error {
	if m.Workflow != nil {
		return m.Workflow.Approve(ctx, clipID)
	}
	if err := m.Clips.SetApproval(ctx, clipID, domain.ClipApproved); err != nil {
		return err
	}
	n, err := m.Clips.SchedulePublications(ctx, clipID, m.now())
	slog.InfoContext(ctx, "clip approved", "clip_id", clipID, "publications_scheduled", n)
	return err
}

func (m *Manager) Reject(ctx context.Context, clipID string) error {
	if m.Workflow != nil {
		return m.Workflow.Reject(ctx, clipID)
	}
	slog.InfoContext(ctx, "clip rejected", "clip_id", clipID)
	return m.Clips.SetApproval(ctx, clipID, domain.ClipRejected)
}

func (m *Manager) ListClips(ctx context.Context, approval string) ([]domain.Clip, error) {
	clips, err := m.Clips.ListClips(ctx, approval)
	if err != nil {
		return nil, err
	}
	return clips, nil
}

func (m *Manager) AccountPublications(ctx context.Context, accountID int64, kind string) ([]domain.Publication, error) {
	return m.Clips.AccountPublications(ctx, accountID, kind)
}

// Tick sends due publications to gpu-be; call periodically.
func (m *Manager) Tick(ctx context.Context) error {
	if m.Workflow != nil {
		return m.Workflow.Tick(ctx)
	}
	due, err := m.Clips.DuePublications(ctx, m.now(), 50)
	if err != nil {
		return err
	}
	for _, d := range due {
		err := m.Broker.Publish(ctx, msg.QueuePublishClip, msg.PublishClip{
			ClipID: d.ClipID, AccountID: d.AccountID, Platform: d.Platform, PublishAt: d.PublishAt.UTC().Format(time.RFC3339),
		})
		if err != nil {
			slog.ErrorContext(ctx, "publish enqueue failed; will retry", "clip", d.ClipID, "err", err)
			_ = m.Clips.MarkPublication(ctx, d.ClipID, d.AccountID, domain.PubScheduled, "", "")
		}
	}
	return nil
}

// HandleResult records the publisher's answer.
func (m *Manager) HandleResult(ctx context.Context, r msg.PublishResult) error {
	if m.Workflow != nil {
		return nil
	} // Legacy stub results never confirm a real publication.
	status := domain.PubSent // "scheduled" from the publisher means accepted, not yet published
	switch r.Status {
	case "published":
		status = domain.PubPublished
	case "failed":
		status = domain.PubFailed
	}
	slog.InfoContext(ctx, "publish result", "clip_id", r.ClipID, "account_id", r.AccountID, "status", status, "error", r.Error)
	return m.Clips.MarkPublication(ctx, r.ClipID, r.AccountID, status, r.UploadID, r.Error)
}
