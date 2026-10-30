package workflow

import (
	"github.com/google/uuid"
	"terex/main-be/internal/domain"
	"testing"
)

func TestManualPublicationRequiresReviewedSupportedTarget(t *testing.T) {
	valid := domain.ManualPublishRequest{RequestID: uuid.NewString(), RevisionID: 1, Version: 1, MediaKind: "source", AccountID: 1, Platform: "tiktok", Caption: "reviewed", Visibility: "private", Reviewed: true}
	if err := validateManualPublication(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.ManualPublishRequest){
		func(u *domain.ManualPublishRequest) { u.Reviewed = false },
		func(u *domain.ManualPublishRequest) { u.RequestID = "" },
		func(u *domain.ManualPublishRequest) { u.Visibility = "unlisted" },
		func(u *domain.ManualPublishRequest) { u.MediaKind = "https://arbitrary.invalid/video.mp4" },
		func(u *domain.ManualPublishRequest) { u.Platform = "instagram" },
	} {
		u := valid
		change(&u)
		if err := validateManualPublication(u); err == nil {
			t.Fatal("invalid manual request accepted", u)
		}
	}
}
