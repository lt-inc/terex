package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	workflowservice "terex/main-be/internal/service/workflow"
	"terex/proto/twitch"
)

func TestTwitchMetadataPersistsIntroAndReusesRenderedMedia(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	f := flowFixture(t, s, account, false, now)
	f.Language = "ru"
	f.CompilationMinDurationSec, f.CompilationMaxDurationSec = 60, 300
	var err error
	if f, err = s.SaveTwitchFlow(ctx, f, false, now); err != nil {
		t.Fatal(err)
	}
	items := []twitch.CompilationItemRequest{}
	for n, login := range []string{"alpha", "beta", "alpha"} {
		source := flowSource("metadata-"+login+strings.Repeat("x", n), "123", 20, now)
		source.BroadcasterLogin = login
		im := finishFlow(t, s, reserveFlow(t, s, f, source, now), 20, "", now)
		items = append(items, twitch.CompilationItemRequest{SourceID: im.ImportID, EndSec: 20})
	}
	comp, err := s.CreateCompilation(ctx, twitch.CompilationRequest{FlowID: f.ID, Title: "Тест подборки", Items: items}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	completeRender(t, s, comp.RevisionID, now, nil)
	rev, err := s.LatestRevision(ctx, comp.ClipID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.MetadataSnapshot == nil || strings.Contains(rev.Description, "clips.twitch.tv") || !strings.Contains(rev.Description, twitch.MetadataContactEmail) {
		t.Fatal("missing generated metadata", rev.Description)
	}
	service := workflowservice.Service{Repo: s, Clips: s}
	request := domain.EditRequest{Version: rev.Version, EditSpec: rev.EditSpec}
	snapshot := *rev.MetadataSnapshot
	snapshot.Intro, snapshot.IntroEdited = "Моё вступление", true
	request.MetadataSnapshot = &snapshot
	request.Description = "injected automatic blocks"
	request.MetadataSnapshot.Sources = []twitch.MetadataSource{{Login: "attacker"}}
	editor, err := service.SaveEditor(ctx, comp.ClipID, request)
	if err != nil {
		t.Fatal(err)
	}
	if editor.Object != rev.Object || editor.RenderState != "ready" || !strings.HasPrefix(editor.Description, "Моё вступление") || strings.Contains(editor.Description, "attacker") {
		t.Fatal("metadata edit changed media or source attribution", editor)
	}
	preview, err := s.TwitchMetadataInput(ctx, domain.TwitchMetadataPreviewRequest{ClipID: comp.ClipID, RevisionID: editor.ID, Title: editor.Title})
	if err != nil || twitch.GenerateMetadata(preview).Description != editor.Description {
		t.Fatal("preview mismatch", err)
	}
	// A title-only builder save must also reuse the already assembled file.
	comp, err = s.UpdateCompilation(ctx, comp.ClipID, domain.CompilationUpdate{ExpectedVersion: editor.Version, CompilationRequest: twitch.CompilationRequest{FlowID: f.ID, Title: "Новое название", Items: items}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if comp.State != "ready" || comp.Object != rev.Object {
		t.Fatal("title-only compilation rerendered", comp)
	}
	items = []twitch.CompilationItemRequest{items[1], items[0], items[2]}
	comp, err = s.UpdateCompilation(ctx, comp.ClipID, domain.CompilationUpdate{ExpectedVersion: comp.Version, CompilationRequest: twitch.CompilationRequest{FlowID: f.ID, Title: comp.Title, Items: items}}, now)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.LatestRevision(ctx, comp.ClipID)
	if err != nil || !strings.HasPrefix(changed.Description, "Моё вступление") || strings.Index(changed.Description, "twitch.tv/beta") > strings.Index(changed.Description, "twitch.tv/alpha") || strings.Count(changed.Description, "twitch.tv/alpha") != 1 {
		t.Fatal("composition did not preserve intro and ordered unique credits", changed.Description, err)
	}
	if changed.RenderState != "pending" {
		t.Fatal("changed assembly did not request render")
	}
	if err = validateGeneratedPublication(domain.EditSpec{Title: "valid", Description: strings.Repeat("я", 2501), MetadataSnapshot: changed.MetadataSnapshot}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("oversized publication accepted", err)
	}
}

func TestTwitchMetadataErrorDoesNotUndoVerifiedImport(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	f := flowFixture(t, s, account, false, now)
	f.Metadata = &twitch.MetadataSettings{ShortIntroTemplate: strings.Repeat("{title}", 30)}
	var err error
	if f, err = s.SaveTwitchFlow(ctx, f, false, now); err != nil {
		t.Fatal(err)
	}
	source := flowSource("long-metadata-source", "123", 20, now)
	source.Title = strings.Repeat("я", 90)
	im := finishFlow(t, s, reserveFlow(t, s, f, source, now), 20, "", now)
	if im.MediaState != "ready" || im.ClipID != "" {
		t.Fatal("metadata failure lost verified source", im)
	}
	page, err := s.ListFlowClips(ctx, f.ID, domain.FlowClipQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Reason.Code != "metadata_invalid" {
		t.Fatal("metadata waiting reason missing", page, err)
	}
	f.Metadata = nil
	if _, err = s.SaveTwitchFlow(ctx, f, false, now); err != nil {
		t.Fatal(err)
	}
	if err = s.ActFlowSource(ctx, f.ID, im.ImportID, "retry", false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	im, err = s.GetFlowImport(ctx, im.ImportID, f.ID)
	if err != nil || im.ClipID == "" || im.MediaState != "ready" {
		t.Fatal("settings fix did not produce release", im, err)
	}
}
