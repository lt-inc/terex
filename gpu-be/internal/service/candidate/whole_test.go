package candidate

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
)

type sourceStore struct{ data string }

func (s sourceStore) Download(_ context.Context, _ domain.ObjectRef, path string) error {
	return os.WriteFile(path, []byte(s.data), 0o644)
}
func (sourceStore) Upload(context.Context, domain.ObjectRef, string) error { return nil }
func (sourceStore) Delete(context.Context, domain.ObjectRef) error         { return nil }
func (sourceStore) Put(context.Context, domain.ObjectRef, io.Reader, int64) error {
	return nil
}
func (sourceStore) PresignGet(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}
func (sourceStore) PresignPut(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}

func TestWholeIntervalCoversVideo(t *testing.T) {
	s := &Service{Store: sourceStore{`{"duration_sec": 42.5}`}}
	iv, err := s.whole(context.Background(), "job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(iv) != 1 || iv[0].Start != 0 || iv[0].End != 42.5 {
		t.Fatalf("unexpected intervals %+v", iv)
	}
}

func TestWholeRejectsUnknownDuration(t *testing.T) {
	s := &Service{Store: sourceStore{`{}`}}
	if _, err := s.whole(context.Background(), "job", t.TempDir()); err == nil {
		t.Fatal("expected an error for a missing duration")
	}
}
