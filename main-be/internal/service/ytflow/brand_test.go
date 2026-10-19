package ytflow

import (
	"testing"

	"terex/main-be/internal/domain"
)

func TestApplyBrandOffLeavesCardUntouched(t *testing.T) {
	card := domain.VideoCard{}
	applyBrand(&card, domain.YTFlowConfig{BrandBackground: "bg", BrandBanner: "bn"})
	if card.Brand != nil || card.AddBackground || len(card.Banners) != 0 {
		t.Fatalf("brand off must not change the job: %+v", card)
	}
}

func TestApplyBrandSetsLayoutBackgroundAndBanner(t *testing.T) {
	card := domain.VideoCard{Targets: []domain.AccountTarget{{AccountID: 7}}}
	applyBrand(&card, domain.YTFlowConfig{BrandStyle: true, BrandBackground: "bg", BrandBanner: "bn"})
	if card.Brand == nil || card.Brand.VideoScale != 0.75 || card.Brand.PitchSemitones != 2 {
		t.Fatalf("unexpected brand %+v", card.Brand)
	}
	if !card.AddBackground || card.BackgroundName != "bg" {
		t.Fatalf("background not applied: %+v", card)
	}
	if len(card.Banners) != 1 || card.Banners[0].BannerName != "bn" || card.Banners[0].Targets[0].AccountID != 7 {
		t.Fatalf("banner variant must publish to the flow's account: %+v", card.Banners)
	}
}

func TestApplyBrandBlurAndNoBanner(t *testing.T) {
	card := domain.VideoCard{}
	applyBrand(&card, domain.YTFlowConfig{BrandStyle: true})
	if card.Brand == nil || card.AddBackground || card.BackgroundName != "" || len(card.Banners) != 0 {
		t.Fatalf("blur without banner expected: %+v", card)
	}
}
