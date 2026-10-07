package publishworker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/credentials"
	"terex/gpu-be/internal/repository/youtube"
	"terex/gpu-be/internal/service/googleauth"
	"terex/proto/workflow"
)

var ErrLeaseLost = errors.New("workflow lease is no longer valid")

type Worker struct {
	Coordinator      workflow.Coordinator
	Store            domain.ObjectStore
	Auth             *googleauth.Service
	TmpDir, WorkerID string
	Now              func() time.Time
	Provider         func(youtube.TokenSource) *youtube.Client
	ProviderFactory  func(string, youtube.TokenSource) (domain.PublishingProvider, error)
	RenewInterval    time.Duration
}

func (w *Worker) Handle(ctx context.Context, cmd workflow.Command) error {
	lease, err := w.Coordinator.Claim(ctx, &workflow.ClaimRequest{Command: cmd, WorkerID: w.WorkerID})
	if err != nil {
		return err
	}
	if !lease.Granted {
		return nil
	}
	if lease.Publication == nil {
		return fmt.Errorf("coordinator returned no publication")
	}
	q := workflow.LeaseRequest{AttemptID: lease.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: lease.ExpectedVersion}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	lost := false
	cancelRequested := false
	check := func() error {
		mu.Lock()
		defer mu.Unlock()
		if lost {
			return ErrLeaseLost
		}
		return nil
	}
	// Renew while downloading and transferring. Loss of approval or coordinator
	// connectivity stops the HTTP transfer and prevents a later visibility update.
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := w.RenewInterval
		if interval <= 0 {
			interval = 10 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p, e := w.Coordinator.Renew(ctx, &q)
				if e != nil || !p.Allowed {
					mu.Lock()
					lost = true
					mu.Unlock()
					cancel()
					return
				}
				if !p.CanPublish || p.State == "cancel_pending" || p.State == "cancelled" {
					mu.Lock()
					cancelRequested = true
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	err = w.execute(ctx, q, lease.Publication, check)
	cancel()
	<-done
	mu.Lock()
	wasCancelled := cancelRequested
	mu.Unlock()
	if wasCancelled {
		reportCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer stop()
		cp := lease.Publication.Checkpoint
		cp.State = "cancelled"
		_, e := w.Coordinator.Complete(reportCtx, &workflow.CompleteRequest{LeaseRequest: q, Checkpoint: cp})
		return e
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrLeaseLost) || check() != nil {
		return nil
	}
	// A process shutdown deliberately leaves the lease to expire. The coordinator
	// reclaims it and probes the persisted YouTube session on the next attempt.
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	failure := workflow.FailRequest{LeaseRequest: q, State: "retry_wait", Code: "provider_error", Message: err.Error(), Retryable: true, RetryAfterSeconds: 30}
	if errors.Is(err, domain.ErrUploadUncertain) {
		failure.State = "needs_attention"
		failure.Code = "upload_outcome_unknown"
		failure.Retryable = false
	}
	if errors.Is(err, domain.ErrReauthentication) {
		failure.State = "blocked"
		failure.Code = "reauthentication_required"
		failure.Retryable = false
		failure.ReconnectRequired = true
	}
	var api *youtube.APIError
	if errors.As(err, &api) {
		failure.Retryable = api.Retryable()
		if !failure.Retryable {
			failure.State = "blocked"
		}
		if api.Status == 401 {
			failure.ReconnectRequired = true
		}
		if api.RetryAfter > 0 {
			failure.RetryAfterSeconds = int(api.RetryAfter.Seconds())
		}
	}
	// Do not report through the cancelled work context; the lease still fences the call.
	reportCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	_, reportErr := w.Coordinator.Fail(reportCtx, &failure)
	return reportErr
}

func (w *Worker) execute(ctx context.Context, q workflow.LeaseRequest, p *workflow.PublicationWork, check func() error) error {
	now := w.Now
	if now == nil {
		now = time.Now
	}
	visibility := p.Visibility
	if visibility == "" {
		visibility = "public"
	}
	if visibility != "public" && visibility != "private" && visibility != "unlisted" {
		return domain.ErrUnsupported
	}
	if p.Provider != "youtube" && w.ProviderFactory == nil {
		return domain.ErrUnsupported
	}
	if now().Before(p.PublishAt) {
		return fmt.Errorf("publication was claimed before its upload time")
	}
	if p.State == "cancel_pending" && p.Checkpoint.RemoteID == "" {
		cp := p.Checkpoint
		cp.State = "cancelled"
		r, e := w.Coordinator.Complete(ctx, &workflow.CompleteRequest{LeaseRequest: q, Checkpoint: cp})
		if e != nil {
			return e
		}
		if !r.Accepted {
			return ErrLeaseLost
		}
		return nil
	}
	id := ""
	var err error
	if p.Provider == "youtube" {
		id, err = credentials.ID(p.CredentialRef)
		if err != nil {
			return err
		}
		c, e := w.Auth.Check(ctx, id)
		if e != nil {
			return e
		}
		if c.ChannelID != p.RemoteAccountID || c.ChannelID == "" {
			return fmt.Errorf("OAuth channel does not match scheduled account")
		}
	}
	token := func(ctx context.Context) (string, error) {
		if w.Auth == nil || id == "" {
			return "", domain.ErrUnsupported
		}
		return w.Auth.AccessToken(ctx, id)
	}
	var provider domain.PublishingProvider
	if w.ProviderFactory != nil {
		provider, err = w.ProviderFactory(p.Provider, token)
		if err != nil {
			return err
		}
	} else {
		newProvider := w.Provider
		if newProvider == nil {
			newProvider = youtube.New
		}
		provider = newProvider(token)
	}
	metadata := domain.PublicationMetadata{Title: p.Metadata.Title, Description: p.Metadata.Description, Tags: p.Metadata.Hashtags, CategoryID: p.Metadata.CategoryID, MadeForKids: p.Metadata.MadeForKids, ContainsSyntheticMedia: p.Metadata.ContainsSyntheticMedia}
	if err = provider.Validate(metadata); err != nil {
		return &youtube.APIError{Status: 400, Reason: err.Error()}
	}
	cp := p.Checkpoint
	persist := func() error {
		if err := check(); err != nil {
			return err
		}
		r, e := w.Coordinator.Checkpoint(ctx, &workflow.CheckpointRequest{LeaseRequest: q, Checkpoint: cp})
		if e != nil {
			return e
		}
		if !r.Accepted {
			return ErrLeaseLost
		}
		p.Checkpoint = cp
		return nil
	}
	complete := func(state string) error {
		cp.State = state
		r, e := w.Coordinator.Complete(ctx, &workflow.CompleteRequest{LeaseRequest: q, Checkpoint: cp})
		if e != nil {
			return e
		}
		if !r.Accepted {
			return ErrLeaseLost
		}
		return nil
	}
	if cp.RemoteID == "" {
		permit, e := w.Coordinator.Renew(ctx, &q)
		if e != nil {
			return e
		}
		if !permit.Allowed {
			return ErrLeaseLost
		}
		if !permit.CanPublish || permit.State == "cancel_pending" || permit.State == "cancelled" {
			return complete("cancelled")
		}
		file := ""
		size := int64(0)
		sourceURL := ""
		if provider.Capabilities().PullFromURL {
			sourceURL, err = w.Store.PresignGet(ctx, p.Media, 2*time.Hour)
			if err != nil {
				return err
			}
		} else {
			if err = os.MkdirAll(w.TmpDir, 0o755); err != nil {
				return err
			}
			dir, e := os.MkdirTemp(w.TmpDir, "publish-")
			if e != nil {
				return e
			}
			defer os.RemoveAll(dir)
			file = filepath.Join(dir, "clip.mp4")
			if err = w.Store.Download(ctx, p.Media, file); err != nil {
				return err
			}
			st, e := os.Stat(file)
			if e != nil {
				return e
			}
			size = st.Size()
			if cp.TotalBytes > 0 && cp.TotalBytes != size {
				return fmt.Errorf("immutable rendered artifact size changed")
			}
		}
		prepare := func() error {
			// Downloading may cross the freshness deadline or outlive a settings
			// change. Ask synchronously at the external session boundary rather
			// than relying on the background renewal's last observation.
			if e := check(); e != nil {
				return e
			}
			permit, e := w.Coordinator.Renew(ctx, &q)
			if e != nil {
				return e
			}
			if !permit.Allowed {
				return ErrLeaseLost
			}
			if !permit.CanPublish || permit.State == "cancel_pending" || permit.State == "cancelled" {
				if e = complete("cancelled"); e != nil {
					return e
				}
				return ErrLeaseLost
			}
			var session domain.UploadSession
			if provider.Capabilities().PullFromURL {
				urlPreparer, ok := provider.(domain.URLUploadPreparer)
				if !ok {
					return domain.ErrUnsupported
				}
				session, e = urlPreparer.PrepareURL(ctx, metadata, sourceURL)
			} else {
				session, e = provider.Prepare(ctx, metadata, size)
			}
			if e != nil {
				return e
			}
			cp.State = "uploading"
			cp.SessionURI = session.URI
			cp.TotalBytes = session.Size
			cp.ConfirmedBytes = 0
			cp.FinalRequestStarted = false
			return persist()
		}
		if cp.SessionURI == "" {
			if err = prepare(); err != nil {
				return err
			}
		}
		transfer := func() error {
			_, e := provider.Transfer(ctx, file, domain.UploadSession{URI: cp.SessionURI, Offset: cp.ConfirmedBytes, Size: cp.TotalBytes, FinalRequestStarted: cp.FinalRequestStarted}, func(s domain.UploadSession) error {
				cp.ConfirmedBytes = s.Offset
				cp.FinalRequestStarted = cp.FinalRequestStarted || s.FinalRequestStarted
				if s.VideoID != "" {
					cp.RemoteID = s.VideoID
					if p.Provider == "youtube" {
						cp.RemoteURL = "https://www.youtube.com/watch?v=" + s.VideoID
					}
					t := now()
					cp.UploadCompletedAt = &t
					cp.State = "processing"
				}
				return persist()
			})
			return e
		}
		if err = transfer(); errors.Is(err, domain.ErrUploadUncertain) && !cp.FinalRequestStarted {
			// A last-byte request was never begun, so this expired session cannot have
			// created a completed video. Start a new private upload and persist it first.
			if err = prepare(); err == nil {
				err = transfer()
			}
		}
		if err != nil {
			return err
		}
		if cp.RemoteID == "" {
			return domain.ErrUploadUncertain
		}
	}
	v, err := provider.Inspect(ctx, cp.RemoteID)
	if err != nil {
		return err
	}
	if v.Privacy == visibility && visibility != "private" {
		t := now()
		cp.VerifiedAt = &t
		return complete("published")
	}
	if v.UploadStatus == "failed" || v.UploadStatus == "rejected" || v.ProcessingStatus == "failed" {
		return &youtube.APIError{Status: 400, Reason: "video processing failed: " + v.Failure}
	}
	if v.ProcessingStatus != "succeeded" && v.UploadStatus != "processed" {
		return complete("processing")
	}
	t := now()
	cp.ProcessingCompletedAt = &t
	cp.State = "ready"
	if err = persist(); err != nil {
		return err
	}
	permit, err := w.Coordinator.Renew(ctx, &q)
	if err != nil {
		return err
	}
	if !permit.Allowed {
		return ErrLeaseLost
	}
	if !permit.CanPublish {
		return complete("cancelled")
	}
	finalizer, ok := provider.(domain.PublishFinalizer)
	if !provider.Capabilities().DeferredVisibility || !ok {
		return complete("processing")
	}
	cp.State = "publishing"
	if err = persist(); err != nil {
		return err
	}
	if visibility == "public" {
		err = finalizer.MakePublic(ctx, cp.RemoteID)
	} else if selected, ok := provider.(domain.VisibilityFinalizer); ok {
		err = selected.SetVisibility(ctx, cp.RemoteID, visibility)
	} else {
		return domain.ErrUnsupported
	}
	if err != nil {
		return err
	}
	verified, err := provider.Inspect(ctx, cp.RemoteID)
	if err != nil {
		return err
	}
	if verified.Privacy != visibility {
		return &youtube.APIError{Status: 403, Reason: "provider did not confirm requested visibility"}
	}
	t = now()
	cp.VerifiedAt = &t
	return complete("published")
}
