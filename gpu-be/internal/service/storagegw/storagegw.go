// Package storagegw is the storage gateway main-be uses over gRPC: presigned URLs and auth files.
package storagegw

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"terex/gpu-be/internal/domain"
)

const defaultTTL = 15 * time.Minute

type Service struct{ Store domain.ObjectStore }

func ttl(sec int32) time.Duration {
	if sec <= 0 {
		return defaultTTL
	}
	return time.Duration(sec) * time.Second
}

func (s *Service) PresignGet(ctx context.Context, ref domain.ObjectRef, ttlSec int32) (string, error) {
	if ref.Bucket == domain.BucketCredentials {
		return "", fmt.Errorf("OAuth credentials are available only through the connection gateway")
	}
	return s.Store.PresignGet(ctx, ref, ttl(ttlSec))
}

// PresignPut returns a URL for direct browser upload; the object name is made unique.
func (s *Service) PresignPut(ctx context.Context, kind, fileName string, ttlSec int32) (string, domain.ObjectRef, error) {
	var bucket string
	switch kind {
	case "banner":
		bucket = domain.BucketBanners
	case "background":
		bucket = domain.BucketBackgrounds
	default:
		return "", domain.ObjectRef{}, fmt.Errorf("unsupported asset kind %q", kind)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	ref := domain.ObjectRef{Bucket: bucket, Object: hex.EncodeToString(b) + "_" + path.Base(fileName)}
	u, err := s.Store.PresignPut(ctx, ref, ttl(ttlSec))
	return u, ref, err
}

func (s *Service) PutAuthFile(ctx context.Context, platform, account string, content []byte) (domain.ObjectRef, error) {
	clean := func(v string) string { return strings.NewReplacer("/", "_", "..", "_").Replace(v) }
	ref := domain.ObjectRef{Bucket: domain.BucketCookies, Object: clean(platform) + "/" + clean(account) + ".json"}
	return ref, s.Store.Put(ctx, ref, bytes.NewReader(content), int64(len(content)))
}

func (s *Service) Delete(ctx context.Context, ref domain.ObjectRef) error {
	if ref.Bucket == domain.BucketCredentials {
		return fmt.Errorf("OAuth credentials must be disconnected through the connection gateway")
	}
	return s.Store.Delete(ctx, ref)
}
