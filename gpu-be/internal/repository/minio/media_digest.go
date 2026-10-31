package minio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	mc "github.com/minio/minio-go/v7"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
)

// MediaDigest reads the actual stored bytes over the internal S3 connection.
// ETag binds GET to the object inspected by HEAD; it is never used as SHA256.
func (s *Store) MediaDigest(ctx context.Context, ref domain.ObjectRef) (*workflow.MediaDigest, error) {
	st, err := s.internal.StatObject(ctx, ref.Bucket, ref.Object, mc.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	if st.Size <= 0 || st.Size > workflow.MaxPublicationMediaBytes || st.ETag == "" {
		return nil, fmt.Errorf("video must contain 1..%d bytes and have an object version", workflow.MaxPublicationMediaBytes)
	}
	opts := mc.GetObjectOptions{}
	if err = opts.SetMatchETag(st.ETag); err != nil {
		return nil, err
	}
	obj, err := s.internal.GetObject(ctx, ref.Bucket, ref.Object, opts)
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(obj, workflow.MaxPublicationMediaBytes+1))
	if err != nil {
		return nil, err
	}
	if n != st.Size || n <= 0 || n > workflow.MaxPublicationMediaBytes {
		return nil, fmt.Errorf("video size changed during verification")
	}
	return &workflow.MediaDigest{SHA256: hex.EncodeToString(h.Sum(nil)), SizeBytes: n}, nil
}
