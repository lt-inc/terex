package renderworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/renderer"
	"terex/proto/workflow"
)

type testCoordinator struct {
	workflow.Coordinator
	lease    *workflow.Lease
	permit   *workflow.Permit
	renewErr error
	failErr  error
	failure  *workflow.FailRequest
}

func (c *testCoordinator) Claim(context.Context, *workflow.ClaimRequest) (*workflow.Lease, error) {
	return c.lease, nil
}

func (c *testCoordinator) Renew(context.Context, *workflow.LeaseRequest) (*workflow.Permit, error) {
	return c.permit, c.renewErr
}

func (c *testCoordinator) Fail(_ context.Context, q *workflow.FailRequest) (*workflow.Result, error) {
	c.failure = q
	return &workflow.Result{Accepted: c.failErr == nil}, c.failErr
}

type failureStore struct {
	domain.ObjectStore
	err error
}

func (s failureStore) Download(context.Context, domain.ObjectRef, string) error { return s.err }

func TestHandlePreservesRenderErrorWhenFailureReportFails(t *testing.T) {
	renderErr := errors.New("source download failed")
	reportErr := errors.New("database connection refused")
	for _, tc := range []struct {
		name      string
		reportErr error
	}{
		{name: "reported failure remains an ACK"},
		{name: "report error retains both causes", reportErr: reportErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinator := &testCoordinator{
				lease: &workflow.Lease{
					Granted: true, AttemptID: "attempt", LeaseToken: "lease", ExpectedVersion: 1,
					Render: &workflow.RenderWork{
						ClipID: "clip", RevisionID: 1, EndSec: 1,
						Source: workflow.SourceManifest{Original: domain.ObjectRef{Bucket: domain.BucketDownloads, Object: "source.mp4"}},
						Output: domain.ObjectRef{Bucket: domain.BucketClips, Object: "revision.mp4"},
					},
				},
				failErr: tc.reportErr,
			}
			worker := Worker{
				Coordinator: coordinator,
				Renderer:    &renderer.Service{Store: failureStore{err: renderErr}, TmpDir: t.TempDir()},
			}
			err := worker.Handle(context.Background(), workflow.Command{})
			if tc.reportErr == nil {
				if err != nil {
					t.Fatalf("persisted terminal failure must remain an ACK: %v", err)
				}
			} else if !errors.Is(err, renderErr) || !errors.Is(err, reportErr) || !strings.Contains(err.Error(), "report render failure:") {
				t.Fatalf("lost original render or failure-report cause: %v", err)
			}
			if coordinator.failure == nil || coordinator.failure.Message != "source unavailable: "+renderErr.Error() || coordinator.failure.Code != "render_failed" || coordinator.failure.Retryable {
				t.Fatalf("incorrect persisted render failure: %+v", coordinator.failure)
			}
		})
	}
}

func TestRenewalCancellationDistinguishesDependencyFailureAndDeniedLease(t *testing.T) {
	rpcErr := errors.New("coordinator database unavailable")
	for _, tc := range []struct {
		name         string
		permit       *workflow.Permit
		rpcErr       error
		continueWork bool
		wantErr      bool
	}{
		{name: "allowed", permit: &workflow.Permit{Allowed: true}, continueWork: true},
		{name: "denied", permit: &workflow.Permit{}},
		{name: "RPC failure", rpcErr: rpcErr, wantErr: true},
		{name: "RPC deadline", rpcErr: context.DeadlineExceeded, wantErr: true},
		{name: "missing permit", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			worker := Worker{Coordinator: &testCoordinator{permit: tc.permit, renewErr: tc.rpcErr}}
			if got := worker.renewRenderLease(ctx, cancel, &workflow.LeaseRequest{}); got != tc.continueWork {
				t.Fatalf("continue rendering = %v, want %v", got, tc.continueWork)
			}
			if tc.continueWork {
				if ctx.Err() != nil {
					t.Fatal("valid lease cancelled rendering")
				}
				return
			}
			select {
			case <-ctx.Done():
			default:
				t.Fatal("lost lease did not stop rendering immediately")
			}
			err := renderCancellationError(ctx)
			if tc.wantErr {
				if !errors.Is(err, errRenderLeaseRenewFailed) || (tc.rpcErr != nil && !errors.Is(err, tc.rpcErr)) {
					t.Fatalf("renewal cause must reach consumer trace: %v", err)
				}
			} else if err != nil || !errors.Is(context.Cause(ctx), errRenderLeaseDenied) {
				t.Fatalf("denied lease must remain an expected ACK: %v", err)
			}
		})
	}
}

func TestParentShutdownRemainsExpectedAck(t *testing.T) {
	parent, shutdown := context.WithCancelCause(context.Background())
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	shutdown(errors.New("service shutdown"))
	// A renewal in flight may return context.Canceled after the parent stops.
	worker := Worker{Coordinator: &testCoordinator{renewErr: context.Canceled}}
	worker.renewRenderLease(ctx, cancel, &workflow.LeaseRequest{})
	if err := renderCancellationError(ctx); err != nil {
		t.Fatalf("parent shutdown must remain an expected ACK: %v", err)
	}
}

func TestRenewalFailureSurvivesLaterParentShutdown(t *testing.T) {
	parent, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	rpcErr := errors.New("coordinator database unavailable")
	worker := Worker{Coordinator: &testCoordinator{renewErr: rpcErr}}
	worker.renewRenderLease(ctx, cancel, &workflow.LeaseRequest{})
	shutdown()
	if err := renderCancellationError(ctx); !errors.Is(err, rpcErr) {
		t.Fatalf("later parent shutdown hid the renewal failure: %v", err)
	}
}
