package twitch

import (
	"context"
	"fmt"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

func (s *Service) MetadataPreview(ctx context.Context, request domain.TwitchMetadataPreviewRequest) (twitch.MetadataResult, error) {
	repo, ok := s.Repo.(domain.TwitchMetadataRepo)
	if !ok {
		return twitch.MetadataResult{}, fmt.Errorf("metadata repository is unavailable")
	}
	input, err := repo.TwitchMetadataInput(ctx, request)
	if err != nil {
		return twitch.MetadataResult{}, err
	}
	return twitch.GenerateMetadata(input), nil
}
