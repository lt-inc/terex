package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"terex/main-be/internal/domain"
	t "terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

func (s *Service) flows() (domain.TwitchFlowRepo, error) {
	r, ok := s.Repo.(domain.TwitchFlowRepo)
	if !ok {
		return nil, fmt.Errorf("flow repository is unavailable")
	}
	return r, nil
}
func (s *Service) Flows(ctx context.Context) ([]t.Flow, error) {
	r, e := s.flows()
	if e != nil {
		return nil, e
	}
	return r.ListTwitchFlows(ctx)
}
func (s *Service) Flow(ctx context.Context, id string) (t.Flow, error) {
	r, e := s.flows()
	if e != nil {
		return t.Flow{}, e
	}
	return r.GetTwitchFlow(ctx, id)
}
func (s *Service) SaveFlow(ctx context.Context, f t.Flow, create bool) (t.Flow, error) {
	r, e := s.flows()
	if e != nil {
		return f, e
	}
	return r.SaveTwitchFlow(ctx, f, create, s.now())
}
func (s *Service) DeleteFlow(ctx context.Context, id string) error {
	r, e := s.flows()
	if e != nil {
		return e
	}
	return r.DeleteTwitchFlow(ctx, id, s.now())
}
func (s *Service) CollectorConfig(ctx context.Context) (t.CollectorConfig, error) {
	if repo, ok := s.Repo.(interface {
		TwitchCollectorConfig(context.Context) (t.CollectorConfig, error)
	}); ok {
		return repo.TwitchCollectorConfig(ctx)
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return t.CollectorConfig{}, e
	}
	var flows []t.Flow
	if repo, ok := s.Repo.(interface {
		ListCollectorTwitchFlows(context.Context) ([]t.Flow, error)
	}); ok {
		flows, e = repo.ListCollectorTwitchFlows(ctx)
	} else {
		flows, e = s.Flows(ctx)
	}
	if e != nil {
		return t.CollectorConfig{}, e
	}
	for n := range flows {
		if flows[n].BlockReason != "" {
			flows[n].Enabled = false
		}
	}
	out := t.CollectorConfig{Settings: settings, Flows: flows}
	if repo, ok := s.Repo.(interface {
		TwitchCollectorReset(context.Context) (int64, *t.ResetMaintenance, error)
	}); ok {
		out.Epoch, out.Maintenance, e = repo.TwitchCollectorReset(ctx)
		if e != nil {
			return out, e
		}
	} else if repo, ok := s.Repo.(interface {
		TwitchReset(context.Context) (t.ResetStatus, error)
	}); ok {
		reset, err := repo.TwitchReset(ctx)
		if err != nil {
			return out, err
		}
		out.Epoch = reset.Epoch
		if reset.State != "idle" && reset.State != "completed" {
			out.Maintenance = &t.ResetMaintenance{ID: reset.ID, Epoch: reset.Epoch, State: reset.State}
		}
	}
	if repo, ok := s.Repo.(interface {
		BlockedTwitchSources(context.Context) ([]string, error)
	}); ok {
		out.BlockedSourceIDs, e = repo.BlockedTwitchSources(ctx)
	}
	return out, e
}
func (s *Service) Observations(ctx context.Context, q t.ObservationRequest) ([]t.Import, error) {
	if q.FlowID == "" || q.FlowVersion < 1 || len(q.Sources) > 100 {
		return nil, domain.ErrInvalid
	}
	for _, src := range q.Sources {
		if e := ValidateImport(t.ImportRequest{SchemaVersion: 2, FlowID: q.FlowID, FlowVersion: q.FlowVersion, IdempotencyKey: "observe:" + src.ClipID, Source: src}, s.now()); e != nil {
			return nil, e
		}
	}
	r, e := s.flows()
	if e != nil {
		return nil, e
	}
	return r.ObserveTwitchSources(ctx, q, s.now())
}
func (s *Service) FlowClips(ctx context.Context, id string, q domain.FlowClipQuery) (domain.FlowClipPage, error) {
	r, e := s.flows()
	if e != nil {
		return domain.FlowClipPage{}, e
	}
	return r.ListFlowClips(ctx, id, q)
}
func (s *Service) ActSource(ctx context.Context, flow, id, action string, excluded bool, expectedGeneration ...int64) error {
	r, e := s.flows()
	if e != nil {
		return e
	}
	return r.ActFlowSource(ctx, flow, id, action, excluded, s.now(), expectedGeneration...)
}
func (s *Service) FlowImports(ctx context.Context, id string) ([]t.Import, error) {
	r, e := s.flows()
	if e != nil {
		return nil, e
	}
	return r.ListFlowImports(ctx, id)
}
func (s *Service) FlowImport(ctx context.Context, id, flow string) (t.Import, error) {
	r, e := s.flows()
	if e != nil {
		return t.Import{}, e
	}
	return r.GetFlowImport(ctx, id, flow)
}
func (s *Service) AutoFlowCompilation(ctx context.Context, id string) (domain.AutoCompilationResult, error) {
	r, e := s.flows()
	if e != nil {
		return domain.AutoCompilationResult{}, e
	}
	return r.AutoFlowCompilation(ctx, id, s.now())
}
func (s *Service) SourcePreview(ctx context.Context, id, flow string) (domain.Preview, error) {
	r, e := s.flows()
	if e != nil {
		return domain.Preview{}, e
	}
	ref, e := r.TwitchSourcePreview(ctx, id, flow)
	if e != nil {
		return domain.Preview{}, e
	}
	return s.presignPreview(ctx, ref, id, 0, "source")
}
func (s *Service) RevisionPreview(ctx context.Context, id string, rid int64, kind string) (domain.Preview, error) {
	r, ok := s.Repo.(interface {
		RevisionByID(context.Context, int64) (domain.Revision, error)
	})
	if !ok {
		return domain.Preview{}, domain.ErrNotFound
	}
	if rid <= 0 {
		return domain.Preview{}, fmt.Errorf("%w: укажите точную ревизию", domain.ErrInvalid)
	}
	rev, e := r.RevisionByID(ctx, rid)
	if e != nil {
		return domain.Preview{}, e
	}
	if rev.ClipID != id {
		return domain.Preview{}, domain.ErrNotFound
	}
	if available, ok := s.Repo.(interface {
		TwitchRevisionMediaAvailable(context.Context, int64) error
	}); ok {
		if e = available.TwitchRevisionMediaAvailable(ctx, rid); e != nil {
			return domain.Preview{}, e
		}
	}
	ref := workflow.ObjectRef{Bucket: rev.Bucket, Object: rev.Object}
	if kind == "source" {
		var m workflow.SourceManifest
		if e = json.Unmarshal(rev.Manifest, &m); e != nil {
			return domain.Preview{}, e
		}
		if m.Assembly != nil {
			return domain.Preview{}, fmt.Errorf("%w: выберите исходник подборки", domain.ErrInvalid)
		}
		ref = m.Original
	} else if kind != "rendered" {
		return domain.Preview{}, domain.ErrInvalid
	} else if rev.RenderState != "ready" {
		return domain.Preview{}, fmt.Errorf("%w: обработка ролика ещё не завершена", domain.ErrConflict)
	}
	return s.presignPreview(ctx, ref, id, rid, kind)
}
func (s *Service) presignPreview(ctx context.Context, ref workflow.ObjectRef, id string, rid int64, kind string) (domain.Preview, error) {
	if ref.Bucket == "" || ref.Object == "" || s.Gpu == nil {
		return domain.Preview{}, fmt.Errorf("%w: видео пока недоступно", domain.ErrConflict)
	}
	previewURL, e := s.Gpu.PresignGet(ctx, ref.Bucket, ref.Object)
	expires := s.now().Add(time.Hour)
	if parsed, err := url.Parse(previewURL); err == nil {
		q := parsed.Query()
		signed, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
		ttl, errTTL := strconv.Atoi(q.Get("X-Amz-Expires"))
		if err == nil && errTTL == nil && ttl > 0 {
			expires = signed.Add(time.Duration(ttl) * time.Second)
		}
	}
	return domain.Preview{URL: previewURL, ExpiresAt: expires, ClipID: id, RevisionID: rid, Kind: kind}, e
}
