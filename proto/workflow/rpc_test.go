package workflow

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"testing"
	"time"
)

type testCoordinator struct{ captured *CheckpointRequest }

func (*testCoordinator) Claim(_ context.Context, q *ClaimRequest) (*Lease, error) {
	return &Lease{Granted: true, AttemptID: q.AttemptID, Publication: &PublicationWork{PublicationID: q.PublicationID, RevisionID: q.RevisionID, CredentialRef: ObjectRef{Bucket: "credentials", Object: "connection.json"}}}, nil
}
func (*testCoordinator) Renew(_ context.Context, q *LeaseRequest) (*Permit, error) {
	if q.LeaseToken == "expired" {
		return nil, status.Error(codes.Aborted, "lease expired")
	}
	return &Permit{Allowed: true, CanPublish: true}, nil
}
func (s *testCoordinator) Checkpoint(_ context.Context, q *CheckpointRequest) (*Result, error) {
	s.captured = q
	return &Result{Accepted: true}, nil
}
func (*testCoordinator) Complete(context.Context, *CompleteRequest) (*Result, error) {
	return &Result{Accepted: true}, nil
}
func (*testCoordinator) Fail(context.Context, *FailRequest) (*Result, error) {
	return &Result{Accepted: true}, nil
}

func TestCoordinatorWireRoundTrip(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	impl := &testCoordinator{}
	RegisterCoordinatorServer(s, impl)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	cc, err := grpc.NewClient("passthrough:///workflow", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	c := NewCoordinatorClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, err := c.Claim(ctx, &ClaimRequest{Command: Command{PublicationID: 99, RevisionID: 12, AttemptID: "attempt-a", ExpectedVersion: 4}})
	if err != nil || !lease.Granted || lease.Publication.RevisionID != 12 || lease.AttemptID != "attempt-a" {
		t.Fatalf("lease=%+v err=%v", lease, err)
	}
	q := &CheckpointRequest{LeaseRequest: LeaseRequest{AttemptID: "attempt-a", LeaseToken: "token", ExpectedVersion: 4}, Checkpoint: Checkpoint{State: "uploading", SessionURI: "https://upload.example/session", ConfirmedBytes: 262144, TotalBytes: 400000, FinalRequestStarted: true}}
	if _, err = c.Checkpoint(ctx, q); err != nil {
		t.Fatal(err)
	}
	if impl.captured.SessionURI != q.SessionURI || !impl.captured.FinalRequestStarted || impl.captured.ExpectedVersion != 4 {
		t.Fatalf("lost checkpoint %+v", impl.captured)
	}
	if _, err = c.Renew(ctx, &LeaseRequest{LeaseToken: "expired"}); status.Code(err) != codes.Aborted {
		t.Fatalf("lost error code: %v", err)
	}
}
