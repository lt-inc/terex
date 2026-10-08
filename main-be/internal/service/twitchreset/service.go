package twitchreset

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/storagelifecycle"
	"terex/proto/twitch"
	"terex/proto/workflow"
)

type Service struct {
	Repo    domain.TwitchResetRepo
	Storage *storagelifecycle.Service
	Gateway workflow.ResetGateway
	mu      sync.Mutex
}

func New(repo domain.TwitchResetRepo, storage *storagelifecycle.Service, gateway workflow.ResetGateway) *Service {
	return &Service{Repo: repo, Storage: storage, Gateway: gateway}
}
func (s *Service) Preview(ctx context.Context) (twitch.ResetPreview, error) {
	if err := s.Storage.RefreshInventory(ctx); err != nil {
		return twitch.ResetPreview{}, err
	}
	return s.Repo.PreviewTwitchReset(ctx, time.Now().UTC())
}
func (s *Service) Start(ctx context.Context, q twitch.ResetRequest) (twitch.ResetStatus, error) {
	return s.Repo.StartTwitchReset(ctx, q, time.Now().UTC())
}
func (s *Service) Status(ctx context.Context) (twitch.ResetStatus, error) {
	return s.Repo.TwitchReset(ctx)
}
func (s *Service) AcknowledgeCollector(ctx context.Context, q twitch.ResetCollectorAck) error {
	return s.Repo.AcknowledgeTwitchReset(ctx, q, time.Now().UTC())
}
func (s *Service) Tick(ctx context.Context) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.Status(ctx)
	if err != nil {
		return err
	}
	active := out.State != "idle" && out.State != "completed"
	if s.Gateway == nil {
		return fmt.Errorf("Twitch reset GPU gateway is unavailable")
	}
	rpcCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	drained, gpuErr := s.Gateway.SetTwitchMaintenance(rpcCtx, &workflow.TwitchMaintenanceRequest{Epoch: out.Epoch, Active: active})
	cancel()
	if !active {
		return gpuErr
	}
	defer func() {
		if err != nil {
			_ = s.Repo.FailTwitchReset(context.WithoutCancel(ctx), out.ID, err.Error(), time.Now().UTC())
		}
		status, e := s.Status(context.WithoutCancel(ctx))
		if e == nil {
			slog.InfoContext(ctx, "twitch reset progress", "reset_id", status.ID, "state", status.State, "epoch", status.Epoch, "collector_ack", status.CollectorAck, "gpu_drained", status.GPUDrained, "deleted_objects", status.DeletedObjects, "remaining_objects", status.RemainingObjects, "deleted_bytes", status.DeletedBytes, "preserved_objects", status.PreservedObjects, "error", status.Error)
		}
	}()
	if gpuErr != nil {
		return gpuErr
	}
	if drained == nil {
		return fmt.Errorf("Twitch reset GPU drain returned no status")
	}
	if out.State == "stopping" || out.State == "failed" {
		out, err = s.Repo.AdvanceTwitchReset(ctx, out.ID, drained.Drained, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	if out.State == "stopping" {
		return nil
	}
	if !drained.Drained {
		return fmt.Errorf("Twitch GPU writers have not drained")
	}
	if out.State == "deleting" {
		if err = s.Storage.RefreshInventory(ctx); err != nil {
			return err
		}
		if err = s.Repo.QueueTwitchResetObjects(ctx, out.ID, time.Now().UTC()); err != nil {
			return err
		}
		objects, e := s.Repo.PendingTwitchResetObjects(ctx, out.ID, 200)
		if e != nil {
			return e
		}
		for _, v := range objects {
			allowed, e := s.Repo.CheckTwitchResetObject(ctx, out.ID, v.Ref)
			if e != nil {
				return e
			}
			if !allowed {
				continue
			}
			rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			response, deleteErr := s.Storage.Gateway.DeleteTwitchObject(rpcCtx, &workflow.StorageDeleteRequest{Ref: v.Ref})
			cancel()
			if deleteErr == nil && (response == nil || !response.Accepted) {
				deleteErr = fmt.Errorf("Twitch object deletion was not accepted")
			}
			if e = s.Repo.FinishTwitchResetObject(ctx, out.ID, v.Ref, deleteErr, time.Now().UTC()); e != nil {
				return e
			}
			if deleteErr != nil {
				slog.WarnContext(ctx, "twitch reset object deletion retry", "bucket", v.Ref.Bucket, "object", v.Ref.Object, "error", deleteErr)
			}
		}
		out, err = s.Status(ctx)
		if err != nil {
			return err
		}
		if out.RemainingObjects > 0 {
			return nil
		}
		// Confirm the physical scan again, rather than clearing metadata merely
		// because a previous DELETE response was lost or a page was incomplete.
		if err = s.Storage.RefreshInventory(ctx); err != nil {
			return err
		}
		if err = s.Repo.QueueTwitchResetObjects(ctx, out.ID, time.Now().UTC()); err != nil {
			return err
		}
		out, err = s.Status(ctx)
		if err != nil {
			return err
		}
		if out.RemainingObjects > 0 {
			return nil
		}
	}
	if err = s.Repo.ClearTwitchReset(ctx, out.ID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}
