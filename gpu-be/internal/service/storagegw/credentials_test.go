package storagegw

import (
	"context"
	"testing"

	"terex/gpu-be/internal/domain"
)

func TestLegacyGatewayCannotAccessOAuthBucket(t *testing.T) {
	s := &Service{}
	ref := domain.ObjectRef{Bucket: domain.BucketCredentials, Object: "google/1.json"}
	if _, err := s.PresignGet(context.Background(), ref, 60); err == nil {
		t.Fatal("credential presigning allowed")
	}
	if err := s.Delete(context.Background(), ref); err == nil {
		t.Fatal("credential deletion bypass allowed")
	}
}
