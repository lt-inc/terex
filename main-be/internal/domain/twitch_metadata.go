package domain

import (
	"context"
	"terex/proto/twitch"
)

// Source references are resolved from Terex, never accepted as public provenance.
type TwitchMetadataPreviewRequest struct {
	FlowID         string                   `json:"flow_id,omitempty"`
	Format         string                   `json:"format"`
	Title          string                   `json:"title"`
	Settings       *twitch.MetadataSettings `json:"settings,omitempty"`
	SourceMode     string                   `json:"source_mode,omitempty"`
	CategoryName   string                   `json:"category_name,omitempty"`
	LanguageFilter string                   `json:"language_filter,omitempty"`
	SourceIDs      []string                 `json:"source_ids,omitempty"`
	Intro          *string                  `json:"intro,omitempty"`
	IntroEdited    *bool                    `json:"intro_edited,omitempty"`
	ClipID         string                   `json:"clip_id,omitempty"`
	RevisionID     int64                    `json:"revision_id,omitempty"`
}

type TwitchMetadataRepo interface {
	TwitchMetadataInput(context.Context, TwitchMetadataPreviewRequest) (twitch.MetadataInput, error)
}
