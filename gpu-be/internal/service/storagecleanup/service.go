// Package storagecleanup is the bounded, Twitch-only storage gateway. Decisions
// about retention and shared references stay in main-be's database.
package storagecleanup

import (
	"context"
	"regexp"
	"strings"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Store interface {
	ListTwitchObjects(context.Context, string, string, string, int) ([]workflow.StorageObject, string, error)
	Delete(context.Context, domain.ObjectRef) error
}
type Service struct{ Store Store }

var twitchRender = regexp.MustCompile(`^(twitch|compilation)-[A-Za-z0-9_-]+/`)

func Allowed(ref workflow.ObjectRef) bool {
	if strings.Contains(ref.Object, "..") || strings.ContainsAny(ref.Object, "\\\x00") {
		return false
	}
	if ref.Bucket == "imports" {
		return strings.HasPrefix(ref.Object, "staging/") || strings.HasPrefix(ref.Object, "verified/") || strings.HasPrefix(ref.Object, "assemblies/twitch-") || strings.HasPrefix(ref.Object, "assemblies/compilation-")
	}
	return ref.Bucket == "clips" && twitchRender.MatchString(ref.Object)
}
func (s *Service) Inventory(ctx context.Context, q *workflow.StorageInventoryRequest) (*workflow.StorageInventoryResponse, error) {
	if q == nil || q.Limit < 1 || q.Limit > 500 || !((q.Bucket == "imports" && (q.Prefix == "staging/" || q.Prefix == "verified/" || q.Prefix == "assemblies/twitch-" || q.Prefix == "assemblies/compilation-")) || (q.Bucket == "clips" && (q.Prefix == "twitch-" || q.Prefix == "compilation-"))) {
		return nil, status.Error(codes.InvalidArgument, "unsupported Twitch inventory prefix or page size")
	}
	objects, next, err := s.Store.ListTwitchObjects(ctx, q.Bucket, q.Prefix, q.After, q.Limit)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "Twitch inventory unavailable: %v", err)
	}
	return &workflow.StorageInventoryResponse{Objects: objects, Next: next}, nil
}
func (s *Service) DeleteTwitchObject(ctx context.Context, q *workflow.StorageDeleteRequest) (*workflow.Result, error) {
	if q == nil || !Allowed(q.Ref) {
		return nil, status.Error(codes.InvalidArgument, "object is outside Twitch storage")
	}
	if err := s.Store.Delete(ctx, q.Ref); err != nil {
		return nil, status.Errorf(codes.Unavailable, "Twitch object deletion failed: %v", err)
	}
	return &workflow.Result{Accepted: true}, nil
}
