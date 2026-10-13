package renderer

import (
	"context"
	"sync"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/mediatmp"
	"terex/proto/workflow"
)

// PreparedRender is local to this attempt. Work alone is sent to Complete;
// filesystem paths and cleanup functions never enter a manifest or queue.
type PreparedRender struct {
	Work           workflow.RenderWork
	MediaSHA256    string
	MediaSizeBytes int64
	originalPath   string
	localAssembly  bool
	ctx            context.Context
	cleanup        func()
	once           sync.Once
}

func (p *PreparedRender) Close() {
	if p != nil {
		p.once.Do(func() {
			if p.cleanup != nil {
				p.cleanup()
			}
		})
	}
}

func (s *Service) PrepareRender(ctx context.Context, w workflow.RenderWork) (*PreparedRender, error) {
	format := "short"
	if w.Source.Assembly != nil {
		format = "compilation"
	}
	ctx = domain.WithMediaIdentity(ctx, w.JobID, w.ClipID, format, w.RevisionID)
	ctx, release, err := mediatmp.Begin(ctx, s.Resources)
	if err != nil {
		return nil, err
	}
	work, local, cleanup, err := s.materializeAssembly(ctx, w, s.AssemblyLocalReuse)
	if err != nil {
		release()
		return nil, mediatmp.Cause(ctx, err)
	}
	return &PreparedRender{Work: work, originalPath: local, localAssembly: local != "", ctx: ctx, cleanup: func() {
		if cleanup != nil {
			cleanup()
		}
		release()
	}}, nil
}
func (s *Service) RenderPrepared(ctx context.Context, p *PreparedRender) (domain.ObjectRef, error) {
	if err := ctx.Err(); err != nil {
		return domain.ObjectRef{}, context.Cause(ctx)
	}
	return s.renderRevision(context.WithValue(p.ctx, renderDigestKey{}, p), p.Work, p.originalPath, p.localAssembly)
}
