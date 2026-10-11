package minio

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
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

// PresignInternalPut grants the collector write access to exactly one staging
// object. The storage credentials never leave this process.
func (s *Store) PresignInternalPut(ctx context.Context, ref domain.ObjectRef, ttl time.Duration) (string, error) {
	u, err := s.internal.PresignedPutObject(ctx, ref.Bucket, ref.Object, ttl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Store) DownloadLimited(ctx context.Context, ref domain.ObjectRef, dest string, limit int64) (err error) {
	start := time.Now()
	defer func() { transferLog(ctx, "download_limited", dest, start, err) }()
	st, err := s.internal.StatObject(ctx, ref.Bucket, ref.Object, mc.StatObjectOptions{})
	if err != nil {
		if mc.ToErrorResponse(err).Code == "NoSuchKey" {
			return domain.ErrObjectNotFound
		}
		return err
	}
	if st.Size <= 0 || st.Size > limit {
		return fmt.Errorf("object must contain 1..%d bytes", limit)
	}
	obj, err := s.internal.GetObject(ctx, ref.Bucket, ref.Object, mc.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(obj, limit+1))
	if err != nil {
		return err
	}
	if n > limit || n != st.Size {
		return fmt.Errorf("object size changed during verification")
	}
	return f.Sync()
}

// Public TLS is independent of the internal MinIO connection. Older callers
// retain their original single-SSL behavior when no public override is supplied.
func New(endpoint, publicEndpoint, access, secret string, ssl bool, publicSSL ...bool) (*Store, error) {
	creds := credentials.NewStaticV4(access, secret, "")
	in, err := mc.New(endpoint, &mc.Options{Creds: creds, Secure: ssl, Region: "us-east-1"})
	if err != nil {
		return nil, err
	}
	securePublic := ssl
	if len(publicSSL) > 0 {
		securePublic = publicSSL[0]
	}
	pub, err := mc.New(publicEndpoint, &mc.Options{Creds: creds, Secure: securePublic, Region: "us-east-1"})
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
	start := time.Now()
	defer func() { transferLog(ctx, "upload", path, start, err) }()
	ctx, span := telemetry.Start(ctx, "minio upload", attribute.String("s3.bucket", ref.Bucket), attribute.String("s3.object", ref.Object))
	defer func() { telemetry.End(span, err) }()
	_, err = s.internal.FPutObject(ctx, ref.Bucket, ref.Object, path, mc.PutObjectOptions{})
	return err
}

func (s *Store) Download(ctx context.Context, ref domain.ObjectRef, path string) (err error) {
	start := time.Now()
	defer func() { transferLog(ctx, "download", path, start, err) }()
	ctx, span := telemetry.Start(ctx, "minio download", attribute.String("s3.bucket", ref.Bucket), attribute.String("s3.object", ref.Object))
	defer func() { telemetry.End(span, err) }()
	return s.internal.FGetObject(ctx, ref.Bucket, ref.Object, path, mc.GetObjectOptions{})
}

func transferLog(ctx context.Context, op, path string, start time.Time, err error) {
	attrs := append(domain.MediaLogAttrs(ctx), "op", op, "duration_ms", time.Since(start).Milliseconds(), "success", err == nil, "result_code", domain.MediaResultCode(domain.MediaCause(ctx, err)))
	if info, e := os.Stat(path); e == nil {
		attrs = append(attrs, "bytes", info.Size())
	}
	slog.InfoContext(ctx, "media storage transfer finished", attrs...)
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
