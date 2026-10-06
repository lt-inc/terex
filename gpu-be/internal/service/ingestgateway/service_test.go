package ingestgateway

import (
	"context"
	"errors"
	"os"
	"strings"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeStore struct {
	data     []byte
	uploaded domain.ObjectRef
	signed   domain.ObjectRef
	failure  string
}

func (s *fakeStore) PresignInternalPut(_ context.Context, r domain.ObjectRef, _ time.Duration) (string, error) {
	if s.failure == "reserve" {
		return "", errors.New("storage temporarily unavailable")
	}
	s.signed = r
	return "http://storage/signed", nil
}
func (s *fakeStore) DownloadLimited(_ context.Context, _ domain.ObjectRef, p string, _ int64) error {
	if s.failure == "download" {
		return errors.New("storage temporarily unavailable")
	}
	return os.WriteFile(p, s.data, 0o600)
}
func (s *fakeStore) Upload(_ context.Context, r domain.ObjectRef, _ string) error {
	if s.failure == "upload" {
		return errors.New("storage temporarily unavailable")
	}
	s.uploaded = r
	return nil
}

type fakeMedia struct {
	probe   Probe
	corrupt bool
}

func (m fakeMedia) Probe(context.Context, string) (Probe, error) { return m.probe, nil }
func (m fakeMedia) VerifyIntegrity(context.Context, string) error {
	if m.corrupt {
		return errors.New("decode failed")
	}
	return nil
}
func TestImportVerificationRejectsInvalidBytes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sha     string
		probe   Probe
		corrupt bool
	}{
		{"digest", strings.Repeat("0", 64), Probe{DurationSec: 10, Width: 640, Height: 480}, false},
		{"audio-only", "", Probe{DurationSec: 10}, false},
		{"long", "", Probe{DurationSec: 70, Width: 640, Height: 480}, false},
		{"truncated", "", Probe{DurationSec: 10, Width: 640, Height: 480}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{data: []byte("fake-video")}
			s := Service{Store: st, Media: fakeMedia{tc.probe, tc.corrupt}, TmpDir: t.TempDir()}
			_, err := s.VerifyUpload(context.Background(), &workflow.VerifyUploadRequest{ImportID: "import-1", Ref: staging("import-1"), ExpectedSHA256: tc.sha})
			if status.Code(err) != codes.InvalidArgument || st.uploaded.Bucket != "" {
				t.Fatalf("invalid media accepted: %v %+v", err, st.uploaded)
			}
		})
	}
}

func TestStorageOutageRemainsRetryable(t *testing.T) {
	for _, phase := range []string{"reserve", "download", "upload"} {
		t.Run(phase, func(t *testing.T) {
			st := &fakeStore{data: []byte("fake-video"), failure: phase}
			s := Service{Store: st, Media: fakeMedia{probe: Probe{DurationSec: 10, Width: 640, Height: 480}}, TmpDir: t.TempDir()}
			var err error
			if phase == "reserve" {
				_, err = s.ReserveUpload(context.Background(), &workflow.ReserveUploadRequest{ImportID: "import-1"})
			} else {
				_, err = s.VerifyUpload(context.Background(), &workflow.VerifyUploadRequest{ImportID: "import-1", Ref: staging("import-1")})
			}
			if status.Code(err) != codes.Unavailable || st.uploaded.Bucket != "" {
				t.Fatalf("storage outage must remain retryable: %v", err)
			}
		})
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s := Service{Store: &fakeStore{failure: "download"}, TmpDir: t.TempDir()}
	_, err := s.VerifyUpload(ctx, &workflow.VerifyUploadRequest{ImportID: "import-1", Ref: staging("import-1")})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("verification timeout classified as permanent input failure: %v", err)
	}
}
func TestVerifiedObjectCannotBeOverwrittenByUploadURL(t *testing.T) {
	st := &fakeStore{data: []byte("fake-video")}
	s := Service{Store: st, Media: fakeMedia{probe: Probe{DurationSec: 10, Width: 640, Height: 480}}, TmpDir: t.TempDir()}
	u, err := s.ReserveUpload(context.Background(), &workflow.ReserveUploadRequest{ImportID: "import-1"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.VerifyUpload(context.Background(), &workflow.VerifyUploadRequest{ImportID: "import-1", Ref: u.Ref})
	if err != nil {
		t.Fatal(err)
	}
	if v.Original == u.Ref || v.Original.Object != "verified/sha256/"+v.SHA256+".mp4" || len(v.SHA256) != 64 {
		t.Fatalf("mutable result: %+v", v)
	}
	alias, err := s.VerifyUpload(context.Background(), &workflow.VerifyUploadRequest{ImportID: "import-2-generation-3", Ref: staging("import-2-generation-3"), ExpectedSHA256: v.SHA256})
	if err != nil || alias.Original != v.Original || alias.SHA256 != v.SHA256 {
		t.Fatalf("identical aliases must share one verified object: %+v, %v", alias, err)
	}
	_, err = s.ReserveUpload(context.Background(), &workflow.ReserveUploadRequest{ImportID: "import-1", Ref: v.Original})
	if err == nil {
		t.Fatal("collector granted overwrite of verified original")
	}
	_, err = s.VerifyUpload(context.Background(), &workflow.VerifyUploadRequest{ImportID: "../credentials", Ref: staging("../credentials")})
	if err == nil {
		t.Fatal("path injection accepted")
	}
}
