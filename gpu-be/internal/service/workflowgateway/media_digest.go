package workflowgateway

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
)

func (s *Service) VerifyPublicationMedia(ctx context.Context, q *workflow.VerifyPublicationMediaRequest) (*workflow.MediaDigest, error) {
	// This gateway only verifies ready rendered media. It cannot read credentials,
	// accept external URLs, change an object, or publish anything.
	if q == nil || q.Media.Bucket != domain.BucketClips || q.Media.Object == "" || strings.Contains(q.Media.Object, "..") {
		return nil, status.Error(codes.InvalidArgument, "invalid rendered video reference")
	}
	store, ok := s.Store.(interface {
		MediaDigest(context.Context, domain.ObjectRef) (*workflow.MediaDigest, error)
	})
	if !ok {
		return nil, fmt.Errorf("media verification is not configured")
	}
	return store.MediaDigest(ctx, q.Media)
}
