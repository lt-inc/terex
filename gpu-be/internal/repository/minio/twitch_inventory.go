package minio

import (
	"context"
	mc "github.com/minio/minio-go/v7"
	"terex/proto/workflow"
)

// ListObjects uses S3 start-after, so each page is bounded even on large stores.
func (s *Store) ListTwitchObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]workflow.StorageObject, string, error) {
	pageCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([]workflow.StorageObject, 0, limit)
	for o := range s.internal.ListObjects(pageCtx, bucket, mc.ListObjectsOptions{Prefix: prefix, Recursive: true, StartAfter: after, MaxKeys: limit + 1}) {
		if o.Err != nil {
			return nil, "", o.Err
		}
		if len(out) == limit {
			return out, out[len(out)-1].Ref.Object, nil
		}
		out = append(out, workflow.StorageObject{Ref: workflow.ObjectRef{Bucket: bucket, Object: o.Key}, SizeBytes: o.Size, ModifiedAt: o.LastModified})
	}
	return out, "", ctx.Err()
}
