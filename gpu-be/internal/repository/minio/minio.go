package minio

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	mc "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/proto/telemetry"
)

type Store struct {
	internal *mc.Client // used for data transfer inside the network
	public   *mc.Client // used only to sign URLs reachable from the browser
}

func New(endpoint, publicEndpoint, access, secret string, ssl bool) (*Store, error) {
	creds := credentials.NewStaticV4(access, secret, "")
	in, err := mc.New(endpoint, &mc.Options{Creds: creds, Secure: ssl, Region: "us-east-1"})
	if err != nil {
		return nil, err
	}
	pub, err := mc.New(publicEndpoint, &mc.Options{Creds: creds, Secure: ssl, Region: "us-east-1"})
	if err != nil {
		return nil, err
	}
	return &Store{internal: in, public: pub}, nil
}

// EnsureBuckets creates the buckets if missing.
func (s *Store) EnsureBuckets(ctx context.Context, buckets ...string) error {
	for _, b := range buckets {
		ok, err := s.internal.BucketExists(ctx, b)
		if err != nil {
			return err
		}
		if !ok {
			if err := s.internal.MakeBucket(ctx, b, mc.MakeBucketOptions{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Upload(ctx context.Context, ref domain.ObjectRef, path string) (err error) {
	ctx, span := telemetry.Start(ctx, "minio upload", attribute.String("s3.bucket", ref.Bucket), attribute.String("s3.object", ref.Object))
	defer func() { telemetry.End(span, err) }()
	_, err = s.internal.FPutObject(ctx, ref.Bucket, ref.Object, path, mc.PutObjectOptions{})
	return err
}

func (s *Store) Download(ctx context.Context, ref domain.ObjectRef, path string) (err error) {
	ctx, span := telemetry.Start(ctx, "minio download", attribute.String("s3.bucket", ref.Bucket), attribute.String("s3.object", ref.Object))
	defer func() { telemetry.End(span, err) }()
	return s.internal.FGetObject(ctx, ref.Bucket, ref.Object, path, mc.GetObjectOptions{})
}

func (s *Store) Delete(ctx context.Context, ref domain.ObjectRef) error {
	return s.internal.RemoveObject(ctx, ref.Bucket, ref.Object, mc.RemoveObjectOptions{})
}

func (s *Store) Put(ctx context.Context, ref domain.ObjectRef, r io.Reader, size int64) error {
	_, err := s.internal.PutObject(ctx, ref.Bucket, ref.Object, r, size, mc.PutObjectOptions{})
	return err
}

// Read is bounded for JSON documents and credentials; videos use streaming FGet.
func (s *Store) Read(ctx context.Context, ref domain.ObjectRef) ([]byte, error) {
	o, err := s.internal.GetObject(ctx, ref.Bucket, ref.Object, mc.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	b, err := io.ReadAll(io.LimitReader(o, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 16*1024*1024 {
		return nil, fmt.Errorf("JSON object exceeds 16 MiB")
	}
	return b, nil
}

func (s *Store) Exists(ctx context.Context, ref domain.ObjectRef) (bool, error) {
	_, err := s.internal.StatObject(ctx, ref.Bucket, ref.Object, mc.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if mc.ToErrorResponse(err).Code == "NoSuchKey" || mc.ToErrorResponse(err).Code == "NoSuchBucket" {
		return false, nil
	}
	return false, err
}

func (s *Store) PresignGet(ctx context.Context, ref domain.ObjectRef, ttl time.Duration) (string, error) {
	u, err := s.public.PresignedGetObject(ctx, ref.Bucket, ref.Object, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Store) PresignPut(ctx context.Context, ref domain.ObjectRef, ttl time.Duration) (string, error) {
	u, err := s.public.PresignedPutObject(ctx, ref.Bucket, ref.Object, ttl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
