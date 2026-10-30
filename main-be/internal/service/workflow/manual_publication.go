package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"terex/main-be/internal/domain"
)

func (s *Service) ClipPublicationOptions(ctx context.Context, clip string) (domain.ClipPublicationOptions, error) {
	if _, err := s.Repo.LatestRevision(ctx, clip); errors.Is(err, domain.ErrNotFound) {
		if err = s.Repo.InitializeRevision(ctx, clip); err != nil {
			return domain.ClipPublicationOptions{}, err
		}
	} else if err != nil {
		return domain.ClipPublicationOptions{}, err
	}
	return s.Repo.ClipPublicationOptions(ctx, clip)
}

func validateManualPublication(u domain.ManualPublishRequest) error {
	if _, err := uuid.Parse(u.RequestID); err != nil || u.RevisionID <= 0 || u.Version <= 0 || u.AccountID <= 0 || !u.Reviewed {
		return fmt.Errorf("%w: Требуются ID запроса, просмотренная версия, её подтверждение и аккаунт", domain.ErrInvalid)
	}
	if u.Platform != "youtube" && u.Platform != "tiktok" {
		return fmt.Errorf("%w: Выберите YouTube или TikTok", domain.ErrInvalid)
	}
	if u.MediaKind != "source" && u.MediaKind != "rendered" {
		return fmt.Errorf("%w: Выберите готовый клип или проверенный оригинал", domain.ErrInvalid)
	}
	if u.Visibility != "private" && u.Visibility != "public" && !(u.Platform == "youtube" && u.Visibility == "unlisted") {
		return fmt.Errorf("%w: Выбранная видимость недоступна для платформы", domain.ErrInvalid)
	}
	limit := 5000
	if u.Platform == "tiktok" {
		limit = 2200
	}
	if strings.TrimSpace(u.Caption) == "" || utf8.RuneCountInString(u.Caption) > limit || len(u.Caption) > 20000 {
		return fmt.Errorf("%w: Подпись должна содержать от 1 до %d символов", domain.ErrInvalid, limit)
	}
	if u.Platform == "youtube" && len(u.Caption) > 5000 {
		return fmt.Errorf("%w: Подпись YouTube превышает 5000 байт", domain.ErrInvalid)
	}
	return nil
}

func (s *Service) PublishClip(ctx context.Context, clip string, u domain.ManualPublishRequest) (domain.PublicationRecord, error) {
	if u.Visibility == "" {
		u.Visibility = "private"
	}
	if err := validateManualPublication(u); err != nil {
		return domain.PublicationRecord{}, err
	}
	return s.Repo.CreateManualPublication(ctx, clip, u, s.now())
}
