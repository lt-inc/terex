package renderer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"terex/gpu-be/internal/service/mediatmp"
	"time"
)

var ErrAssemblyBudget = errors.New("compilation temporary disk budget exceeded")

type scratchKey struct{}
type assemblyScratch struct {
	dir    string
	root   string
	budget int64
}

func (s *Service) assemblyRoot() (string, error) {
	root := s.AssemblyTmpDir
	if root == "" {
		root = filepath.Join(s.TmpDir, "assembly-scratch")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	legacy, err := filepath.Abs(s.TmpDir)
	if err != nil {
		return "", err
	}
	if root == legacy || filepath.Dir(root) == root {
		return "", fmt.Errorf("assembly scratch must be a dedicated directory")
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("assembly scratch must be a real directory: %s", root)
	}
	return root, nil
}

// PrepareAssemblyScratch runs before consumers start. Only directories owned by
// this renderer are removed; legacy YouTube TMP and publisher state are separate.
func (s *Service) PrepareAssemblyScratch() error {
	root, err := s.assemblyRoot()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "attempt-") {
			if err = os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// BeginAssembly holds one permit for materialization AND its following revision
// render. The monitor also counts partial downloads and ffmpeg output in flight.
func (s *Service) BeginAssembly(ctx context.Context) (context.Context, func(), error) {
	s.assemblyOnce.Do(func() { s.assemblyPermit = make(chan struct{}, 1) })
	select {
	case s.assemblyPermit <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	root, err := s.assemblyRoot()
	if err != nil {
		<-s.assemblyPermit
		return nil, nil, err
	}
	dir, cleanup, err := mediatmp.MkdirTemp(ctx, s.Resources, root, "attempt-")
	if err != nil {
		<-s.assemblyPermit
		return nil, nil, err
	}
	budget := s.AssemblyBudgetBytes
	if budget <= 0 {
		budget = 8 << 30
	}
	workCtx, cancel := context.WithCancelCause(ctx)
	workCtx = context.WithValue(workCtx, scratchKey{}, assemblyScratch{dir: dir, root: root, budget: budget})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if err := CheckAssemblyBudget(workCtx); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return workCtx, func() {
		cancel(context.Canceled)
		<-done
		cleanup()
		<-s.assemblyPermit
	}, nil
}

func (s *Service) revisionTmp(ctx context.Context) string {
	if scratch, ok := ctx.Value(scratchKey{}).(assemblyScratch); ok {
		return scratch.dir
	}
	return s.TmpDir
}

func CheckAssemblyBudget(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	scratch, ok := ctx.Value(scratchKey{}).(assemblyScratch)
	if !ok {
		return nil
	}
	var bytes int64
	err := filepath.WalkDir(scratch.root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // a completed source may be removed while the monitor scans
		}
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		bytes += info.Size()
		if bytes > scratch.budget {
			return fmt.Errorf("%w: used %d bytes, limit %d bytes", ErrAssemblyBudget, bytes, scratch.budget)
		}
		return nil
	})
	return err
}
