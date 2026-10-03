package publishworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/credentials"
	"terex/gpu-be/internal/repository/youtube"
	"terex/gpu-be/internal/service/googleauth"
	"terex/proto/workflow"
)

type fakeCoordinator struct {
	work             *workflow.PublicationWork
	checkpoints      []workflow.Checkpoint
	completed        *workflow.CompleteRequest
	failed           *workflow.FailRequest
	permit           workflow.Permit
	rejectCheckpoint bool
}

func (c *fakeCoordinator) Claim(context.Context, *workflow.ClaimRequest) (*workflow.Lease, error) {
	return &workflow.Lease{Granted: true, AttemptID: "attempt", LeaseToken: "lease", ExpectedVersion: 1, Publication: c.work}, nil
}
func (c *fakeCoordinator) Renew(context.Context, *workflow.LeaseRequest) (*workflow.Permit, error) {
	return &c.permit, nil
}
func (c *fakeCoordinator) Checkpoint(_ context.Context, q *workflow.CheckpointRequest) (*workflow.Result, error) {
	c.checkpoints = append(c.checkpoints, q.Checkpoint)
	return &workflow.Result{Accepted: !c.rejectCheckpoint}, nil
}
func (c *fakeCoordinator) Complete(_ context.Context, q *workflow.CompleteRequest) (*workflow.Result, error) {
	c.completed = q
	return &workflow.Result{Accepted: true}, nil
}
func (c *fakeCoordinator) Fail(_ context.Context, q *workflow.FailRequest) (*workflow.Result, error) {
	c.failed = q
	return &workflow.Result{Accepted: true}, nil
}

type fakeStore struct{ downloads, presigns int }

func (s *fakeStore) Download(_ context.Context, _ domain.ObjectRef, p string) error {
	s.downloads++
	return os.WriteFile(p, []byte("video"), 0o600)
}
func (s *fakeStore) Upload(context.Context, domain.ObjectRef, string) error        { return nil }
func (s *fakeStore) Delete(context.Context, domain.ObjectRef) error                { return nil }
func (s *fakeStore) Put(context.Context, domain.ObjectRef, io.Reader, int64) error { return nil }
func (s *fakeStore) PresignGet(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	s.presigns++
	return "https://verified-storage.example/clip.mp4", nil
}
func (s *fakeStore) PresignPut(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}

// URL provider deliberately has no MakePublic or Cancel methods. Its status API
// alone determines publication; URL ingestion never downloads the media locally.
type urlProvider struct {
	prepared, transferred, inspected int
	privacy                          string
	transferErr                      error
	final                            bool
}

func (p *urlProvider) Capabilities() domain.PublishingCapabilities {
	return domain.PublishingCapabilities{PullFromURL: true}
}
func (p *urlProvider) Validate(domain.PublicationMetadata) error { return nil }
func (p *urlProvider) Prepare(context.Context, domain.PublicationMetadata, int64) (domain.UploadSession, error) {
	return domain.UploadSession{}, errors.New("push path must not be used")
}
func (p *urlProvider) PrepareURL(_ context.Context, _ domain.PublicationMetadata, u string) (domain.UploadSession, error) {
	if u == "" {
		return domain.UploadSession{}, errors.New("missing URL")
	}
	p.prepared++
	return domain.UploadSession{URI: "publish-id"}, nil
}
func (p *urlProvider) Transfer(_ context.Context, _ string, s domain.UploadSession, save func(domain.UploadSession) error) (domain.UploadSession, error) {
	p.transferred++
	if p.final {
		s.FinalRequestStarted = true
		if err := save(s); err != nil {
			return s, err
		}
	}
	if p.transferErr != nil {
		return s, p.transferErr
	}
	s.VideoID = "remote"
	return s, save(s)
}
func (p *urlProvider) Inspect(context.Context, string) (domain.RemoteVideo, error) {
	p.inspected++
	return domain.RemoteVideo{ID: "remote", Privacy: p.privacy, ProcessingStatus: "succeeded"}, nil
}

func fixture(t *testing.T, p domain.PublishingProvider) (*Worker, *fakeCoordinator, *fakeStore) {
	t.Helper()
	now := time.Now()
	c := &fakeCoordinator{work: &workflow.PublicationWork{Provider: "mock_url", PublishAt: now.Add(-time.Minute), Metadata: workflow.Metadata{Title: "Title"}}, permit: workflow.Permit{Allowed: true, CanPublish: true}}
	s := &fakeStore{}
	return &Worker{Coordinator: c, Store: s, TmpDir: t.TempDir(), Now: func() time.Time { return now }, ProviderFactory: func(string, youtube.TokenSource) (domain.PublishingProvider, error) { return p, nil }}, c, s
}

func TestCancelBeforeRemoteIDNeedsNoCredentials(t *testing.T) {
	p := &urlProvider{}
	w, c, s := fixture(t, p)
	c.work.Provider = "youtube"
	c.work.State = "cancel_pending"
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if c.completed == nil || c.completed.Checkpoint.State != "cancelled" || s.downloads != 0 || p.prepared != 0 {
		t.Fatal("cancellation required credentials or started upload")
	}
}

func TestURLProviderWithoutFinalizerOrGoogleOAuth(t *testing.T) {
	p := &urlProvider{privacy: "private"}
	w, c, s := fixture(t, p)
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if s.downloads != 0 || s.presigns != 1 || p.prepared != 1 || p.transferred != 1 || c.completed == nil || c.completed.State != "processing" {
		t.Fatalf("unexpected URL flow store=%+v provider=%+v complete=%+v", s, p, c.completed)
	}
	if c.checkpoints[0].SessionURI != "publish-id" {
		t.Fatal("transfer started before durable session")
	}
	p.privacy = "public"
	c.completed = nil
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if p.prepared != 1 || c.completed.State != "published" || c.completed.VerifiedAt == nil {
		t.Fatal("status readback did not complete publication")
	}
}

func TestUploadNotStartedBeforeScheduledTime(t *testing.T) {
	p := &urlProvider{}
	w, c, s := fixture(t, p)
	c.work.PublishAt = time.Now().Add(time.Hour)
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if p.prepared != 0 || s.presigns != 0 || s.downloads != 0 {
		t.Fatal("work began ahead of schedule")
	}
}

func TestCheckpointRejectionPreventsTransfer(t *testing.T) {
	p := &urlProvider{}
	w, c, _ := fixture(t, p)
	c.rejectCheckpoint = true
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if p.transferred != 0 {
		t.Fatal("transfer ran despite rejected lease checkpoint")
	}
}

func TestCancelBeforeTransfer(t *testing.T) {
	p := &urlProvider{}
	w, c, s := fixture(t, p)
	c.permit.State = "cancel_pending"
	c.permit.CanPublish = false
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if p.prepared != 0 || s.presigns != 0 || c.completed == nil || c.completed.State != "cancelled" {
		t.Fatal("cancellation did not stop unstarted upload")
	}
}

func TestAmbiguousFinalUploadIsNotRestarted(t *testing.T) {
	p := &urlProvider{transferErr: domain.ErrUploadUncertain, final: true}
	w, c, _ := fixture(t, p)
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if p.prepared != 1 || c.failed == nil || c.failed.State != "needs_attention" || c.failed.Retryable {
		t.Fatalf("unsafe recovery %+v prepared=%d", c.failed, p.prepared)
	}
}

type finalizerProvider struct {
	urlProvider
	released bool
}

func (p *finalizerProvider) Capabilities() domain.PublishingCapabilities {
	return domain.PublishingCapabilities{PullFromURL: true, DeferredVisibility: true}
}
func (p *finalizerProvider) MakePublic(context.Context, string) error { p.released = true; return nil }
func TestFinalizerSuccessRequiresPublicReadback(t *testing.T) {
	p := &finalizerProvider{urlProvider: urlProvider{privacy: "private"}}
	w, c, _ := fixture(t, p)
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if !p.released || p.inspected != 2 || c.completed != nil || c.failed == nil || c.failed.State != "blocked" {
		t.Fatalf("unverified success accepted inspected=%d complete=%+v fail=%+v", p.inspected, c.completed, c.failed)
	}
}

type credentialRepo struct{ c domain.GoogleCredential }

func (r *credentialRepo) Get(context.Context, string) (domain.GoogleCredential, error) {
	return r.c, nil
}
func (r *credentialRepo) Put(_ context.Context, c domain.GoogleCredential) error { r.c = c; return nil }
func (r *credentialRepo) Delete(context.Context, string) error                   { return nil }

func TestYouTubeWorkerUploadsAtDueTimeThenProcessesAndVerifiesPublic(t *testing.T) {
	var server *httptest.Server
	processing := true
	public := false
	uploads, updates := 0, 0
	server = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels":
			_, _ = res.Write([]byte(`{"items":[{"id":"channel","snippet":{"title":"Channel"}}]}`))
		case "/upload":
			uploads++
			res.Header().Set("Location", server.URL+"/session")
		case "/session":
			if r.ContentLength == 0 {
				res.WriteHeader(308)
			} else {
				res.WriteHeader(201)
				_, _ = res.Write([]byte(`{"id":"video-id"}`))
			}
		case "/videos":
			if r.Method == "PUT" {
				updates++
				public = true
				_, _ = res.Write([]byte(`{"id":"video-id"}`))
				return
			}
			privacy := "private"
			if public {
				privacy = "public"
			}
			status := "processing"
			upload := "uploaded"
			if !processing {
				status = "succeeded"
				upload = "processed"
			}
			_, _ = fmt.Fprintf(res, `{"items":[{"id":"video-id","status":{"privacyStatus":%q,"uploadStatus":%q},"processingDetails":{"processingStatus":%q}}]}`, privacy, upload, status)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			res.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := &urlProvider{}
	w, c, store := fixture(t, p)
	w.ProviderFactory = nil
	c.work.Provider = "youtube"
	c.work.CredentialRef = credentials.Ref("42")
	c.work.RemoteAccountID = "channel"
	factory := func(token youtube.TokenSource) *youtube.Client {
		client := youtube.New(token)
		client.APIURL = server.URL
		client.UploadURL = server.URL + "/upload"
		client.HTTP = server.Client()
		return client
	}
	w.Auth = googleauth.New(&credentialRepo{c: domain.GoogleCredential{AccountID: "42", ChannelID: "channel", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}}, "client", "secret", "https://app.example/callback")
	w.Auth.YouTube = factory
	w.Provider = factory
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if c.completed == nil || c.completed.State != "processing" || updates != 0 {
		t.Fatalf("premature public state completed=%+v updates=%d", c.completed, updates)
	}
	finalBeforeID := false
	for _, cp := range c.checkpoints {
		if cp.FinalRequestStarted && cp.RemoteID == "" {
			finalBeforeID = true
		}
	}
	if !finalBeforeID {
		t.Fatal("final upload ambiguity not persisted")
	}
	processing = false
	c.completed = nil
	if err := w.Handle(context.Background(), workflow.Command{}); err != nil {
		t.Fatal(err)
	}
	if c.completed == nil || c.completed.State != "published" || c.completed.VerifiedAt == nil || uploads != 1 || updates != 1 || store.downloads != 1 {
		t.Fatalf("flow complete=%+v uploads=%d updates=%d downloads=%d", c.completed, uploads, updates, store.downloads)
	}
}
