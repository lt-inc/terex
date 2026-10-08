package twitch

import (
	"context"
	"errors"
	"testing"
	"time"

	"terex/main-be/internal/domain"
)

type abortAdmissionRepo struct {
	domain.TwitchRepo
	domain.TwitchPolicyRepo
	generation int64
	calls      int
}

func (r *abortAdmissionRepo) AbortFlowImport(_ context.Context, _ string, generation int64, _ string, _ time.Time) error {
	r.generation = generation
	r.calls++
	return nil
}

func TestAbortAdmissionBeforeUpload(t *testing.T) {
	r := &abortAdmissionRepo{}
	s := &Service{Repo: r}
	if err := s.AbortImport(context.Background(), "import", 0, "reservation failed"); err != nil || r.calls != 1 || r.generation != 0 {
		t.Fatal("admission could not release its preparation", err, r)
	}
	if err := s.AbortImport(context.Background(), "import", -1, "invalid"); !errors.Is(err, domain.ErrInvalid) || r.calls != 1 {
		t.Fatal("negative generation reached repository", err, r)
	}
}
