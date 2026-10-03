package workflow

import (
	"errors"
	"math"
	"terex/main-be/internal/domain"
	"testing"
)

func TestEditorRejectsInvalidVideoAndCueTimes(t *testing.T) {
	valid := domain.EditSpec{Title: "Title", StartSec: 10, EndSec: 20, Cues: []domain.Cue{{ID: "a", Start: 9, End: 11}, {ID: "b", Start: 19, End: 22}}}
	if err := validateEdit(valid, 30); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*domain.EditSpec){
		func(e *domain.EditSpec) { e.StartSec = math.NaN() },
		func(e *domain.EditSpec) { e.EndSec = 31 },
		func(e *domain.EditSpec) {
			e.Cues = []domain.Cue{{ID: "a", Start: 10, End: 12}, {ID: "b", Start: 11, End: 13}}
		},
		func(e *domain.EditSpec) {
			e.Cues = []domain.Cue{{ID: "a", Start: 10, End: 12}, {ID: "a", Start: 12, End: 13}}
		},
		func(e *domain.EditSpec) { e.Cues = []domain.Cue{{ID: "a", Start: 10, End: math.Inf(1)}} },
	} {
		e := valid
		mutate(&e)
		if err := validateEdit(e, 30); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid edit accepted: %+v %v", e, err)
		}
	}
}

func TestMetadataValidatesVisibilityWithoutRequiringSource(t *testing.T) {
	for _, visibility := range []string{"public", "unlisted", "private", ""} {
		if err := validateMetadata(domain.EditSpec{Title: "Metadata only", Visibility: visibility}); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateMetadata(domain.EditSpec{Title: "Title", Visibility: "friends"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestTargetsRejectDuplicateIDsAndUnknownTimezone(t *testing.T) {
	for _, targets := range [][]domain.AccountTarget{
		{{AccountID: 1}, {AccountID: 1}},
		{{AccountID: 1, Timezone: "Mars/Olympus"}},
		{{AccountID: 1, IntervalSeconds: -1}},
	} {
		if _, err := normalizeTargets(targets); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid targets accepted", err)
		}
	}
	targets, err := normalizeTargets([]domain.AccountTarget{{AccountID: 1, IntervalSeconds: 3600}})
	if err != nil || targets[0].Timezone != "Asia/Tomsk" {
		t.Fatal(targets, err)
	}
}
