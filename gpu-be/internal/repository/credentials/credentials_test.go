package credentials

import (
	"context"
	"io"
	"testing"

	"terex/gpu-be/internal/domain"
)

type memoryStore struct {
	ref  domain.ObjectRef
	data []byte
}

func (s *memoryStore) Read(context.Context, domain.ObjectRef) ([]byte, error) { return s.data, nil }
func (s *memoryStore) Put(_ context.Context, ref domain.ObjectRef, r io.Reader, _ int64) error {
	s.ref = ref
	var err error
	s.data, err = io.ReadAll(r)
	return err
}
func (s *memoryStore) Delete(context.Context, domain.ObjectRef) error { s.data = nil; return nil }
func TestCredentialsUseSeparateBucketAndIdentity(t *testing.T) {
	store := &memoryStore{}
	r := &Repository{Store: store}
	c := domain.GoogleCredential{AccountID: "42", ChannelID: "channel", RefreshToken: "offline-token"}
	if err := r.Put(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if store.ref.Bucket != domain.BucketCredentials || store.ref.Object != "google/42.json" {
		t.Fatalf("ref=%+v", store.ref)
	}
	got, err := r.Get(context.Background(), "42")
	if err != nil || got.RefreshToken != c.RefreshToken {
		t.Fatalf("credential=%+v err=%v", got, err)
	}
	if _, err = r.Get(context.Background(), "43"); err == nil {
		t.Fatal("cross-account JSON accepted")
	}
	if _, err = ID(domain.ObjectRef{Bucket: "cookies", Object: "google/42.json"}); err == nil {
		t.Fatal("legacy cookie accepted as OAuth")
	}
	if err = r.Put(context.Background(), domain.GoogleCredential{AccountID: "../escape"}); err == nil {
		t.Fatal("invalid credential id accepted")
	}
}
