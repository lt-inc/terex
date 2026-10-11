// Package mediaguard bounds scratch shared by all media modules. Only marked
// attempt directories are cleaned; credentials, state and foreign files survive.
package mediaguard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"terex/gpu-be/internal/domain"
)

const marker = ".terex-media-attempt"

type Guard struct {
	Roots                     []string
	BudgetBytes, MinFreeBytes int64
	mu                        sync.Mutex
	next                      uint64
	active                    map[uint64]context.CancelCauseFunc
	FreeBytes                 func(string) (uint64, error)
}

func New(roots []string, budget, free int64) *Guard {
	return &Guard{Roots: roots, BudgetBytes: budget, MinFreeBytes: free, active: map[uint64]context.CancelCauseFunc{}, FreeBytes: freeBytes}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (g *Guard) roots() ([]string, error) {
	var roots []string
	for _, root := range g.Roots {
		p, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		if filepath.Dir(p) == p {
			return nil, fmt.Errorf("scratch cannot be a filesystem root")
		}
		if err = os.MkdirAll(p, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("scratch must be a real directory: %s", p)
		}
		covered := false
		for _, prior := range roots {
			if within(prior, p) {
				covered = true
			}
		}
		if !covered {
			kept := roots[:0]
			for _, prior := range roots {
				if !within(p, prior) {
					kept = append(kept, prior)
				}
			}
			roots = append(kept, p)
		}
	}
	return roots, nil
}

func (g *Guard) Prepare() error {
	roots, err := g.roots()
	if err != nil {
		return err
	}
	for _, root := range roots {
		entries, e := os.ReadDir(root)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if info, e := os.Lstat(filepath.Join(dir, marker)); e == nil && info.Mode().IsRegular() {
				if e = os.RemoveAll(dir); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func (g *Guard) Check() error {
	roots, err := g.roots()
	if err != nil {
		return err
	}
	var total int64
	for _, root := range roots {
		if g.MinFreeBytes > 0 {
			n, e := g.FreeBytes(root)
			if e != nil {
				return e
			}
			if n < uint64(g.MinFreeBytes) {
				return &domain.MediaResourceError{Code: "scratch_free_space", Detail: fmt.Sprintf("available %d bytes, required %d", n, g.MinFreeBytes)}
			}
		}
		err = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !e.Type().IsRegular() {
				return nil
			}
			info, err := e.Info()
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			total += info.Size()
			if g.BudgetBytes > 0 && total > g.BudgetBytes {
				return &domain.MediaResourceError{Code: "scratch_budget", Detail: fmt.Sprintf("used %d bytes, limit %d", total, g.BudgetBytes)}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Guard) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := g.Check(); err != nil {
				if !errors.Is(err, domain.ErrMediaResourceWait) {
					err = &domain.MediaResourceError{Code: "scratch_check", Detail: err.Error()}
				}
				g.mu.Lock()
				for _, cancel := range g.active {
					cancel(err)
				}
				g.mu.Unlock()
			}
		}
	}
}

func (g *Guard) Begin(ctx context.Context) (context.Context, func(), error) {
	if ctx.Err() != nil {
		return ctx, nil, context.Cause(ctx)
	}
	if err := g.Check(); err != nil {
		return ctx, nil, err
	}
	work, cancel := context.WithCancelCause(ctx)
	g.mu.Lock()
	g.next++
	id := g.next
	g.active[id] = cancel
	g.mu.Unlock()
	var once sync.Once
	return work, func() { once.Do(func() { cancel(nil); g.mu.Lock(); delete(g.active, id); g.mu.Unlock() }) }, nil
}

func (g *Guard) MkdirTemp(ctx context.Context, root, prefix string) (string, func(), error) {
	if ctx.Err() != nil {
		return "", nil, context.Cause(ctx)
	}
	if err := g.Check(); err != nil {
		return "", nil, err
	}
	allowed := false
	roots, err := g.roots()
	if err != nil {
		return "", nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	for _, r := range roots {
		if within(r, abs) {
			allowed = true
		}
	}
	if !allowed {
		return "", nil, fmt.Errorf("temporary directory is outside configured scratch roots")
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(abs, prefix)
	if err != nil {
		return "", nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, marker), nil, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	var once sync.Once
	return dir, func() {
		once.Do(func() {
			if e := os.RemoveAll(dir); e != nil {
				slog.Error("media scratch cleanup failed", "err", e)
			}
		})
	}, nil
}
