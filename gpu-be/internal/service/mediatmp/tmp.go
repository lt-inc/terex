package mediatmp

import (
	"context"
	"os"

	"terex/gpu-be/internal/domain"
)

func Begin(ctx context.Context, r domain.MediaResources) (context.Context, func(), error) {
	if r != nil {
		return r.Begin(ctx)
	}
	return ctx, func() {}, nil
}
func MkdirTemp(ctx context.Context, r domain.MediaResources, root, prefix string) (string, func(), error) {
	if r != nil {
		return r.MkdirTemp(ctx, root, prefix)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
func Cause(ctx context.Context, err error) error {
	return domain.MediaCause(ctx, err)
}
