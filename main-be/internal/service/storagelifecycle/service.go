package storagelifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"terex/main-be/internal/domain"
	"terex/proto/workflow"
	"time"
)

type Service struct {
	Repo                domain.TwitchStorageRepo
	Gateway             workflow.StorageGateway
	Enabled             bool
	mu                  sync.Mutex
	lastInventory       time.Time
	complete            bool
	pressureMu          sync.Mutex
	pressureURL         string
	pressureMinBytes    int64
	pressureMinPercent  float64
	pressureLastChecked time.Time
}

func New(repo domain.TwitchStorageRepo, gateway workflow.StorageGateway, enabled bool) *Service {
	return &Service{Repo: repo, Gateway: gateway, Enabled: enabled}
}
func (s *Service) refresh(ctx context.Context, force bool) error {
	if !force && s.complete && time.Since(s.lastInventory) < time.Minute {
		return nil
	}
	if s.Gateway == nil {
		return fmt.Errorf("Twitch storage gateway is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	objects := []workflow.StorageObject{}
	for _, p := range [][2]string{{"imports", "staging/"}, {"imports", "verified/"}, {"imports", "assemblies/twitch-"}, {"imports", "assemblies/compilation-"}, {"clips", "twitch-"}, {"clips", "compilation-"}} {
		after := ""
		for page := 0; page < 100; page++ {
			out, err := s.Gateway.Inventory(ctx, &workflow.StorageInventoryRequest{Bucket: p[0], Prefix: p[1], After: after, Limit: 500})
			if err != nil {
				s.complete = false
				return err
			}
			objects = append(objects, out.Objects...)
			if out.Next == "" {
				break
			}
			if out.Next == after || page == 99 {
				s.complete = false
				return fmt.Errorf("Twitch storage inventory is incomplete; cleanup paused")
			}
			after = out.Next
		}
	}
	now := time.Now().UTC()
	if err := s.Repo.SyncTwitchStorage(ctx, objects, now); err != nil {
		s.complete = false
		return err
	}
	s.lastInventory = now
	s.complete = true
	return nil
}
func (s *Service) stats(ctx context.Context) (domain.TwitchStorageStats, error) {
	if err := s.refresh(ctx, false); err != nil {
		return domain.TwitchStorageStats{}, err
	}
	out, err := s.Repo.TwitchStorage(ctx, time.Now().UTC())
	out.CleanupEnabled = s.Enabled
	out.InventoryComplete = s.complete
	out.UpdatedAt = s.lastInventory
	return out, err
}
func (s *Service) Stats(ctx context.Context) (domain.TwitchStorageStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats(ctx)
}
func (s *Service) Preview(ctx context.Context) (domain.TwitchStorageStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.stats(ctx)
	if err != nil {
		return out, err
	}
	for _, v := range out.Items {
		if v.State != "deleted" && v.ProtectedReason == "" && !v.ExpiresAt.After(time.Now().UTC()) {
			out.Candidates = append(out.Candidates, v)
		}
	}
	return out, nil
}
func (s *Service) Cleanup(ctx context.Context, execute bool) (domain.TwitchCleanupResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := domain.TwitchCleanupResult{DryRun: !execute || !s.Enabled}
	if err := s.refresh(ctx, true); err != nil {
		return out, err
	}
	stats, err := s.Repo.TwitchStorage(ctx, time.Now().UTC())
	if err != nil {
		return out, err
	}
	out.InventoryComplete = s.complete
	out.ReclaimableBytes = stats.ReclaimableBytes
	if out.DryRun {
		slog.InfoContext(ctx, "twitch storage cleanup calculated", "dry_run", true, "inventory_complete", s.complete, "objects", stats.Objects, "physical_bytes", stats.PhysicalBytes, "reclaimable_bytes", stats.ReclaimableBytes, "protected_objects", stats.ProtectedObjects)
		return out, nil
	}
	out.Queued, err = s.Repo.QueueTwitchCleanup(ctx, time.Now().UTC(), 500)
	if err != nil {
		return out, err
	}
	refs, err := s.Repo.PendingTwitchDeletion(ctx, time.Now().UTC(), 500)
	if err != nil {
		return out, err
	}
	for _, ref := range refs {
		allowed, e := s.Repo.CheckTwitchDeletion(ctx, ref, time.Now().UTC())
		if e != nil {
			return out, e
		}
		if !allowed {
			continue
		}
		response, deleteErr := s.Gateway.DeleteTwitchObject(ctx, &workflow.StorageDeleteRequest{Ref: ref})
		e = deleteErr
		if e == nil && (response == nil || !response.Accepted) {
			e = fmt.Errorf("Twitch object deletion was not accepted")
		}
		if e != nil {
			out.Failed++
			slog.WarnContext(ctx, "twitch storage object deletion failed", "bucket", ref.Bucket, "object", ref.Object, "error", e, "retry_after_minutes", 30)
		} else {
			out.Deleted++
		}
		if err = s.Repo.FinishTwitchDeletion(ctx, ref, e, time.Now().UTC()); err != nil {
			return out, err
		}
	}
	s.lastInventory = time.Time{}
	s.complete = false
	slog.InfoContext(ctx, "twitch storage cleanup finished", "dry_run", false, "inventory_complete", out.InventoryComplete, "objects", stats.Objects, "physical_bytes", stats.PhysicalBytes, "protected_objects", stats.ProtectedObjects, "queued", out.Queued, "deleted", out.Deleted, "failed", out.Failed, "reclaimable_bytes", out.ReclaimableBytes)
	return out, nil
}
func (s *Service) Tick(ctx context.Context) error { _, err := s.Cleanup(ctx, s.Enabled); return err }
func (s *Service) KeepImport(ctx context.Context, id string, keep bool) error {
	return s.Repo.KeepTwitchStorage(ctx, "import", id, keep)
}
func (s *Service) KeepClip(ctx context.Context, id string, keep bool) error {
	return s.Repo.KeepTwitchStorage(ctx, "clip", id, keep)
}
