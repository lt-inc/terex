package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/publicationmetadata"
	"terex/proto/twitch"
	"terex/proto/workflow"
)

type metadataQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func metadataSource(ctx context.Context, q metadataQuery, id, clip string) (twitch.Source, error) {
	var data []byte
	err := q.QueryRow(ctx, `SELECT request FROM twitch_imports WHERE ($1<>'' AND (id=$1 OR twitch_clip_id=$1)) OR ($1='' AND clip_id=$2) ORDER BY CASE WHEN id=$1 THEN 0 ELSE 1 END LIMIT 1`, id, clip).Scan(&data)
	if err != nil {
		return twitch.Source{}, mapErr(err)
	}
	var request twitch.ImportRequest
	if err = json.Unmarshal(data, &request); err != nil {
		return twitch.Source{}, err
	}
	return request.Source, nil
}

func assemblyMetadataSources(ctx context.Context, q metadataQuery, assembly *workflow.AssemblySpec) ([]twitch.Source, error) {
	sources := []twitch.Source{}
	if assembly == nil {
		return sources, nil
	}
	for _, item := range assembly.Items {
		source, err := metadataSource(ctx, q, item.TwitchSourceID, item.SourceClipID)
		if err != nil {
			return nil, err
		}
		if item.BroadcasterLogin!="" { source.BroadcasterLogin=item.BroadcasterLogin }
		sources = append(sources, source)
	}
	return sources, nil
}

func flowMetadataInput(f twitch.Flow, format, title string, sources []twitch.Source) twitch.MetadataInput {
	return twitch.MetadataInput{Format: format, Title: title, Settings: f.Metadata, SourceMode: f.SourceMode, CategoryName: f.CategoryName, LanguageFilter: f.Language, Sources: sources}
}

func publicTwitchTitle(title string) string {
	// Twitch titles commonly contain '<3'. Keep the readable title while
	// satisfying YouTube's prohibition on literal angle brackets.
	return strings.NewReplacer("<", "‹", ">", "›").Replace(title)
}

func applyTwitchMetadata(spec *domain.EditSpec, input twitch.MetadataInput) error {
	result := twitch.GenerateMetadata(input)
	if len(result.Errors) > 0 {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, strings.Join(result.Errors, "; "))
	}
	spec.Description, spec.Hashtags, spec.MetadataSnapshot = result.Description, result.Hashtags, &result.Snapshot
	return nil
}

func validateGeneratedPublication(spec domain.EditSpec) error {
	if spec.MetadataSnapshot == nil {
		return nil
	}
	if len(spec.Hashtags) > twitch.MaxMetadataHashtags {
		return fmt.Errorf("%w: максимум %d хештегов", domain.ErrInvalid, twitch.MaxMetadataHashtags)
	}
	if err := publicationmetadata.Validate(spec.Title, publicationmetadata.Compose(spec.Description, spec.Hashtags)); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, err)
	}
	return nil
}

func (s *Store) TwitchMetadataInput(ctx context.Context, req domain.TwitchMetadataPreviewRequest) (twitch.MetadataInput, error) {
	if len(req.SourceIDs) > 60 {
		return twitch.MetadataInput{}, fmt.Errorf("%w: максимум 60 источников", domain.ErrInvalid)
	}
	if req.ClipID != "" {
		var rev domain.Revision
		var err error
		if req.RevisionID > 0 {
			rev, err = s.RevisionByID(ctx, req.RevisionID)
		} else {
			rev, err = s.LatestRevision(ctx, req.ClipID)
		}
		if err != nil {
			return twitch.MetadataInput{}, err
		}
		if rev.ClipID != req.ClipID || rev.MetadataSnapshot == nil {
			return twitch.MetadataInput{}, fmt.Errorf("%w: выпуск не использует шаблон метаданных", domain.ErrInvalid)
		}
		title := req.Title
		if title == "" {
			title = rev.Title
		}
		input := rev.MetadataSnapshot.ToInput(title, nil)
		if req.Settings != nil {
			input.Settings = req.Settings
		}
		if req.Intro != nil {
			input.IntroOverride = req.Intro
		}
		if req.IntroEdited != nil && !*req.IntroEdited {
			input.IntroOverride = nil
		}
		return input, nil
	}
	input := twitch.MetadataInput{Format: req.Format, Title: req.Title, Settings: req.Settings, SourceMode: req.SourceMode, CategoryName: req.CategoryName, LanguageFilter: req.LanguageFilter, Sources: []twitch.Source{}, IntroOverride: req.Intro}
	if req.FlowID != "" {
		f, err := s.GetTwitchFlow(ctx, req.FlowID)
		if err != nil {
			return input, err
		}
		if input.Settings == nil {
			input.Settings = f.Metadata
		}
		if req.SourceMode == "" {
			input.SourceMode, input.CategoryName, input.LanguageFilter = f.SourceMode, f.CategoryName, f.Language
		}
	}
	for _, id := range req.SourceIDs {
		source, err := metadataSource(ctx, s.pool, id, "")
		if err != nil {
			return input, fmt.Errorf("%w: источник %s недоступен для предпросмотра", err, id)
		}
		input.Sources = append(input.Sources, source)
	}
	return input, nil
}
