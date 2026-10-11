package mediaguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"terex/gpu-be/internal/domain"
)

func TestScratchOnlyRemovesOwnedAttemptsAndBoundsSharedUsage(t *testing.T) {
	root := t.TempDir()
	g := New([]string{root}, 20, 10)
	g.FreeBytes = func(string) (uint64, error) { return 100, nil }
	foreign := filepath.Join(root, "state")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "credentials"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	owned, _, err := g.MkdirTemp(context.Background(), root, "revision-")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("owned stale attempt retained")
	}
	if _, err = os.Stat(filepath.Join(foreign, "credentials")); err != nil {
		t.Fatal("foreign data removed", err)
	}
	dir, cleanup, err := g.MkdirTemp(context.Background(), root, "revision-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "source"), make([]byte, 21), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.Check(); !errors.Is(err, domain.ErrMediaResourceWait) {
		t.Fatalf("unbounded scratch: %v", err)
	}
	cleanup()
	cleanup()
	if err = g.Check(); err != nil {
		t.Fatal("cleanup did not free budget", err)
	}
	g.FreeBytes = func(string) (uint64, error) { return 9, nil }
	if _, _, err = g.Begin(context.Background()); !errors.Is(err, domain.ErrMediaResourceWait) {
		t.Fatal("low free space admitted", err)
	}
}
