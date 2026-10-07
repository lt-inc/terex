// Package workflow coordinates immutable clip revisions and durable platform publications.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/google/uuid"
	"terex/main-be/internal/domain"
	w "terex/proto/workflow"
)

type ConfirmedBroker interface {
	PublishConfirmed(context.Context, string, any) error
}
type Service struct {
	Repo        domain.WorkflowRepo
	Coordinator domain.CoordinatorRepo
	Accounts    domain.AccountRepo
	Clips       domain.ClipRepo
	Gpu         domain.Gpu
	Gateway     w.Gateway
	Broker      ConfirmedBroker
	RedirectURL string
	AppURL      string
	Now         func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) InitializeClip(ctx context.Context, id string, manifest json.RawMessage) error {
	if err := s.Repo.InitializeRevision(ctx, id); err != nil {
		return err
	}
	if len(manifest) > 0 && string(manifest) != "null" {
		r, err := s.Repo.LatestRevision(ctx, id)
		if err != nil {
			return err
		}
		if r.Version == 1 {
			return s.Repo.SetRevisionManifest(ctx, r.ID, manifest)
		}
	}
	return nil
}
func (s *Service) Approve(ctx context.Context, id string) error {
	return s.Repo.ApproveRevision(ctx, id, s.now())
}

// Automatic approval only applies to the pipeline's initial revision. A
// duplicate clips-ready event must never approve later manual edits.
func (s *Service) AutoApprove(ctx context.Context, id string) error {
	r, err := s.Repo.LatestRevision(ctx, id)
	if err != nil {
		return err
	}
	if r.Version != 1 || r.ApprovedAt != nil {
		return nil
	}
	err = s.Repo.ApproveRevisionWithVersion(ctx, id, 1, s.now())
	if errors.Is(err, domain.ErrConflict) {
		return nil
	}
	return err
}
func (s *Service) ApproveVersion(ctx context.Context, id string, version int) error {
	if version <= 0 {
		return fmt.Errorf("%w: expected_version is required", domain.ErrInvalid)
	}
	return s.Repo.ApproveRevisionWithVersion(ctx, id, version, s.now())
}
func (s *Service) Reject(ctx context.Context, id string) error { return s.Repo.RejectRevision(ctx, id) }
func (s *Service) Tick(ctx context.Context) error {
	if err := s.Repo.PreparePublications(ctx, s.now(), 50); err != nil {
		return err
	}
	outbox, err := s.Repo.PendingOutbox(ctx, s.now(), 100)
	if err != nil {
		return err
	}
	for _, o := range outbox {
		var cmd w.Command
		if err = json.Unmarshal(o.Payload, &cmd); err != nil {
			return err
		}
		queue := w.QueuePublish
		if o.Kind == "render_revision" {
			queue = w.QueueRender
		}
		sendErr := s.Broker.PublishConfirmed(ctx, queue, cmd)
		if err = s.Repo.MarkOutbox(ctx, o.ID, sendErr); err != nil {
			return err
		}
		if sendErr != nil {
			return sendErr
		}
	}
	return nil
}
func (s *Service) Editor(ctx context.Context, id string) (domain.Editor, error) {
	r, err := s.Repo.LatestRevision(ctx, id)
	if err != nil {
		return domain.Editor{}, err
	}
	clip, err := s.Clips.GetClip(ctx, id)
	if err != nil {
		return domain.Editor{}, err
	}
	var source w.SourceManifest
	if err = json.Unmarshal(r.Manifest, &source); err != nil {
		return domain.Editor{}, err
	}
	// Verified Twitch sources already have immutable timing and no transcript to
	// hydrate. Return editor metadata without issuing short-lived video URLs.
	if source.Assembly != nil || strings.HasPrefix(id, "twitch-") {
		editor := domain.Editor{Revision: r, SourceDurationSec: source.DurationSec, SourceAvailable: source.Original.Object != "" || source.Assembly != nil}
		s.editorHistory(ctx, &editor)
		return editor, nil
	}
	ec, err := s.Gateway.EditorContext(ctx, &w.EditorContextRequest{JobID: clip.JobID, ClipID: id, StartSec: r.StartSec, EndSec: r.EndSec, Source: source})
	if err != nil {
		editor := domain.Editor{Revision: r, SourceDurationSec: source.DurationSec, SourceError: err.Error()}
		s.editorHistory(ctx, &editor)
		return editor, nil
	}
	// Hydration retrieves original timing data; it is not an editor save/revision.
	ec.Source.Cues = ec.Cues
	raw, _ := json.Marshal(ec.Source)
	if err = s.Repo.SetRevisionManifest(ctx, r.ID, raw); err != nil {
		return domain.Editor{}, err
	}
	r, err = s.Repo.LatestRevision(ctx, id)
	if err != nil {
		return domain.Editor{}, err
	}
	if r.Version == 1 && len(r.Cues) == 0 {
		r.Cues = make([]domain.Cue, len(ec.Cues))
		for i, c := range ec.Cues {
			r.Cues[i] = domain.Cue{ID: c.ID, Start: c.Start, End: c.End, Text: c.Text}
		}
	}
	if r.Version == 1 {
		r.SubtitlesEnabled = ec.Source.Options.Subtitles
	}
	editor := domain.Editor{Revision: r, SourceDurationSec: ec.Source.DurationSec, SourceAvailable: ec.SourceAvailable}
	if r.ApprovedAt != nil {
		rid := r.ID
		editor.ApprovedRevisionID = &rid
	}
	s.editorHistory(ctx, &editor)
	return editor, nil
}
func (s *Service) editorHistory(ctx context.Context, e *domain.Editor) {
	e.Revisions = []domain.RevisionSummary{}
	history, err := s.Repo.RevisionHistory(ctx, e.ClipID)
	if err != nil {
		return
	}
	for _, r := range history {
		e.Revisions = append(e.Revisions, domain.RevisionSummary{ID: r.ID, Version: r.Version, Title: r.Title, RenderState: r.RenderState, ApprovedAt: r.ApprovedAt, CreatedAt: r.CreatedAt, Error: r.Error})
	}
}
func (s *Service) RetryRender(ctx context.Context, id string, version int) (domain.Editor, error) {
	if err := s.Repo.RetryRevision(ctx, id, version); err != nil {
		return domain.Editor{}, err
	}
	return s.Editor(ctx, id)
}
func (s *Service) RestoreEditor(ctx context.Context, id string, version int, targetID int64) (domain.Editor, error) {
	if err := s.Repo.RestoreRevision(ctx, id, version, targetID); err != nil {
		return domain.Editor{}, err
	}
	return s.Editor(ctx, id)
}
func validateMetadata(e domain.EditSpec) error {
	if e.Visibility != "" && e.Visibility != "public" && e.Visibility != "unlisted" && e.Visibility != "private" {
		return fmt.Errorf("%w: unsupported visibility", domain.ErrInvalid)
	}
	if strings.TrimSpace(e.Title) == "" || utf8.RuneCountInString(e.Title) > 100 {
		return fmt.Errorf("%w: title must contain 1 to 100 characters", domain.ErrInvalid)
	}
	if len(e.Description) > 5000 {
		return fmt.Errorf("%w: description exceeds 5000 bytes", domain.ErrInvalid)
	}
	return nil
}
func validateEdit(e domain.EditSpec, duration float64) error {
	if err := validateMetadata(e); err != nil {
		return err
	}
	if math.IsNaN(e.StartSec) || math.IsNaN(e.EndSec) || math.IsInf(e.StartSec, 0) || math.IsInf(e.EndSec, 0) || e.StartSec < 0 || e.EndSec <= e.StartSec || duration <= 0 || e.EndSec > duration+0.05 {
		return fmt.Errorf("%w: clip bounds must be within the available source", domain.ErrInvalid)
	}
	ids := map[string]bool{}
	lastEnd := float64(-1)
	for _, c := range e.Cues {
		if c.ID == "" || ids[c.ID] || math.IsNaN(c.Start) || math.IsNaN(c.End) || math.IsInf(c.Start, 0) || math.IsInf(c.End, 0) || c.Start < 0 || c.End <= c.Start || c.End > duration+0.05 || c.Start < lastEnd {
			return fmt.Errorf("%w: cues need unique IDs and ordered, non-overlapping source timings", domain.ErrInvalid)
		}
		ids[c.ID] = true
		lastEnd = c.End
	}
	return nil
}
func (s *Service) SaveEditor(ctx context.Context, id string, u domain.EditRequest) (domain.Editor, error) {
	prev, err := s.Editor(ctx, id)
	if err != nil {
		return domain.Editor{}, err
	}
	if u.Hashtags == nil {
		u.Hashtags = []string{}
	}
	if u.Visibility == "" {
		u.Visibility = prev.Visibility
		if u.Visibility == "" {
			u.Visibility = "public"
		}
	}
	if u.Cues == nil {
		u.Cues = []domain.Cue{}
	}
	if prev.Cues == nil {
		prev.Cues = []domain.Cue{}
	}
	if err = validateMetadata(u.EditSpec); err != nil {
		return domain.Editor{}, err
	}
	rerender := u.StartSec != prev.StartSec || u.EndSec != prev.EndSec || u.SubtitlesEnabled != prev.SubtitlesEnabled || !reflect.DeepEqual(u.Cues, prev.Cues)
	if prev.SourceAvailable && (prev.RenderState != "ready" || prev.Object == "") {
		rerender = true
	}
	if rerender {
		if !prev.SourceAvailable {
			return domain.Editor{}, fmt.Errorf("%w: original source is unavailable; metadata can still be edited", domain.ErrConflict)
		}
		if err = validateEdit(u.EditSpec, prev.SourceDurationSec); err != nil {
			return domain.Editor{}, err
		}
	}
	if _, err = s.Repo.SaveRevision(ctx, id, u, prev.Manifest, rerender); err != nil {
		return domain.Editor{}, err
	}
	return s.Editor(ctx, id)
}
func normalizeTargets(targets []domain.AccountTarget) ([]domain.AccountTarget, error) {
	ids := map[int64]bool{}
	for i := range targets {
		t := &targets[i]
		if t.AccountID <= 0 || ids[t.AccountID] {
			return nil, fmt.Errorf("%w: account targets must be unique", domain.ErrInvalid)
		}
		ids[t.AccountID] = true
		if t.IntervalSeconds < 0 {
			return nil, fmt.Errorf("%w: interval_seconds must be nonnegative", domain.ErrInvalid)
		}
		if t.Timezone == "" {
			t.Timezone = "Asia/Tomsk"
		}
		if _, err := time.LoadLocation(t.Timezone); err != nil {
			return nil, fmt.Errorf("%w: unknown timezone", domain.ErrInvalid)
		}
	}
	return targets, nil
}
func (s *Service) Targets(ctx context.Context, id string) (domain.TargetsResponse, error) {
	t, err := s.Repo.Targets(ctx, id)
	if err != nil {
		return domain.TargetsResponse{}, err
	}
	version, err := s.Repo.TargetVersion(ctx, id)
	return domain.TargetsResponse{Version: version, Targets: t}, err
}
func (s *Service) SaveTargets(ctx context.Context, id string, u domain.TargetsRequest) (domain.TargetsResponse, error) {
	t, err := normalizeTargets(u.Targets)
	if err != nil {
		return domain.TargetsResponse{}, err
	}
	for _, target := range t {
		if _, err = s.Accounts.GetAccount(ctx, target.AccountID); err != nil {
			return domain.TargetsResponse{}, err
		}
	}
	if err = s.Repo.ReplaceTargets(ctx, id, u.Version, t); err != nil {
		return domain.TargetsResponse{}, err
	}
	return s.Targets(ctx, id)
}
func (s *Service) Publications(ctx context.Context, f domain.PublicationFilter) ([]domain.PublicationRecord, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.Repo.ListPublicationRecords(ctx, f)
}
func (s *Service) PatchPublication(ctx context.Context, id int64, u domain.PublicationPatch) (domain.PublicationRecord, error) {
	if u.Version <= 0 {
		return domain.PublicationRecord{}, fmt.Errorf("%w: expected_version is required", domain.ErrInvalid)
	}
	if u.Timezone != nil {
		if _, err := time.LoadLocation(*u.Timezone); err != nil {
			return domain.PublicationRecord{}, fmt.Errorf("%w: unknown timezone", domain.ErrInvalid)
		}
	}
	if u.Title != nil && (strings.TrimSpace(*u.Title) == "" || utf8.RuneCountInString(*u.Title) > 100) {
		return domain.PublicationRecord{}, fmt.Errorf("%w: invalid title", domain.ErrInvalid)
	}
	if u.Description != nil && len(*u.Description) > 5000 {
		return domain.PublicationRecord{}, fmt.Errorf("%w: description exceeds 5000 bytes", domain.ErrInvalid)
	}
	if err := s.Repo.PatchPublication(ctx, id, u); err != nil {
		return domain.PublicationRecord{}, err
	}
	return s.Repo.GetPublicationRecord(ctx, id)
}
func (s *Service) ActPublication(ctx context.Context, id int64, action string, version int) (domain.PublicationRecord, error) {
	return s.ActPublicationWithOverride(ctx, id, action, version, false)
}
func (s *Service) ActPublicationWithOverride(ctx context.Context, id int64, action string, version int, override bool) (domain.PublicationRecord, error) {
	if override {
		r, ok := s.Repo.(interface {
			ActPublicationWithOverride(context.Context, int64, string, int, bool, time.Time) error
		})
		if !ok {
			return domain.PublicationRecord{}, domain.ErrInvalid
		}
		if err := r.ActPublicationWithOverride(ctx, id, action, version, true, s.now()); err != nil {
			return domain.PublicationRecord{}, err
		}
		return s.Repo.GetPublicationRecord(ctx, id)
	}
	if err := s.Repo.ActPublication(ctx, id, action, version, s.now()); err != nil {
		return domain.PublicationRecord{}, err
	}
	return s.Repo.GetPublicationRecord(ctx, id)
}
func (s *Service) Events(ctx context.Context, id int64) ([]domain.PublicationEvent, error) {
	if _, err := s.Repo.GetPublicationRecord(ctx, id); err != nil {
		return nil, err
	}
	return s.Repo.PublicationEvents(ctx, id)
}

func (s *Service) Connect(ctx context.Context, id int64, expectedVersion int) (string, error) {
	a, err := s.Accounts.GetAccount(ctx, id)
	if err != nil {
		return "", err
	}
	if expectedVersion <= 0 || a.ConnectionVersion != expectedVersion {
		return "", domain.ErrConflict
	}
	if a.Platform != "youtube" {
		return "", fmt.Errorf("%w: OAuth publishing currently supports YouTube", domain.ErrInvalid)
	}
	if s.RedirectURL == "" {
		return "", fmt.Errorf("%w: OAuth callback URL is not configured", domain.ErrInvalid)
	}
	state := uuid.NewString()
	if err = s.Repo.PutOAuthState(ctx, state, id, s.RedirectURL, s.now().Add(10*time.Minute)); err != nil {
		return "", err
	}
	u, err := s.Gateway.OAuthURL(ctx, &w.OAuthURLRequest{AccountID: id, State: state, RedirectURL: s.RedirectURL})
	if err != nil {
		return "", err
	}
	return u.URL, nil
}
func (s *Service) OAuthCallback(ctx context.Context, state, code string) (int64, error) {
	if state == "" || code == "" {
		return 0, fmt.Errorf("%w: OAuth state and authorization code are required", domain.ErrInvalid)
	}
	id, redirect, err := s.Repo.ConsumeOAuthState(ctx, state, s.now())
	if err != nil {
		return 0, err
	}
	a, err := s.Accounts.GetAccount(ctx, id)
	if err != nil {
		return id, err
	}
	expected := ""
	if a.RemoteAccountID != nil {
		expected = *a.RemoteAccountID
	}
	connection, err := s.Gateway.OAuthExchange(ctx, &w.OAuthExchangeRequest{AccountID: id, State: state, Code: code, RedirectURL: redirect, ExpectedRemoteID: expected})
	if err != nil {
		ref, refErr := s.Repo.CredentialRef(ctx, id)
		if refErr == nil {
			_ = s.Repo.SetAccountConnection(ctx, id, "needs_reauth", expected, ref, a.ConnectionScopes, a.CredentialExpiresAt, err.Error())
		}
		return id, err
	}
	if a.RemoteAccountID == nil && connection.Status == "connected" {
		connection.Status = "pending_confirmation"
	}
	return id, s.saveConnection(ctx, id, connection)
}
func (s *Service) saveConnection(ctx context.Context, id int64, c *w.Connection) error {
	state := c.Status
	a, err := s.Accounts.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(struct {
		w.ObjectRef
		DisplayName string `json:"display_name,omitempty"`
	}{c.CredentialRef, c.DisplayName})
	remote := c.RemoteID
	scopes := c.Scopes
	expires := c.ExpiresAt
	if state != "connected" && state != "pending_confirmation" {
		state = "needs_reauth"
		if a.RemoteAccountID != nil {
			remote = *a.RemoteAccountID
		}
		raw, err = s.Repo.CredentialRef(ctx, id)
		if err != nil {
			return err
		}
		scopes = a.ConnectionScopes
		expires = a.CredentialExpiresAt
	}
	return s.Repo.SetAccountConnection(ctx, id, state, remote, raw, scopes, expires, c.Error)
}
func (s *Service) CheckConnection(ctx context.Context, id int64) (domain.Account, error) {
	a, err := s.Accounts.GetAccount(ctx, id)
	if err != nil {
		return a, err
	}
	raw, err := s.Repo.CredentialRef(ctx, id)
	if err != nil {
		return a, err
	}
	var ref w.ObjectRef
	_ = json.Unmarshal(raw, &ref)
	expected := ""
	if a.RemoteAccountID != nil {
		expected = *a.RemoteAccountID
	}
	c, err := s.Gateway.CheckConnection(ctx, &w.ConnectionRequest{AccountID: id, CredentialRef: ref, ExpectedRemoteID: expected})
	if err != nil {
		return a, err
	}
	if c.Status == "error" {
		return a, fmt.Errorf("connection check temporarily unavailable: %s", c.Error)
	}
	if a.ConnectionState == "pending_confirmation" && c.Status == "connected" {
		c.Status = "pending_confirmation"
	}
	if err = s.saveConnection(ctx, id, c); err != nil {
		return a, err
	}
	return s.Accounts.GetAccount(ctx, id)
}
func (s *Service) ConfirmConnection(ctx context.Context, id int64, version int, remote string) (domain.Account, error) {
	if remote == "" || version <= 0 {
		return domain.Account{}, fmt.Errorf("%w: expected_version and remote_account_id required", domain.ErrInvalid)
	}
	if err := s.Repo.ConfirmAccountConnection(ctx, id, version, remote); err != nil {
		return domain.Account{}, err
	}
	return s.Accounts.GetAccount(ctx, id)
}
func (s *Service) Disconnect(ctx context.Context, id int64, expectedVersion int) (domain.Account, error) {
	a, err := s.Accounts.GetAccount(ctx, id)
	if err != nil {
		return a, err
	}
	if expectedVersion <= 0 || a.ConnectionVersion != expectedVersion {
		return a, domain.ErrConflict
	}
	raw, err := s.Repo.CredentialRef(ctx, id)
	if err != nil {
		return a, err
	}
	var ref w.ObjectRef
	_ = json.Unmarshal(raw, &ref)
	result, err := s.Gateway.Disconnect(ctx, &w.ConnectionRequest{AccountID: id, CredentialRef: ref})
	if err != nil {
		return a, err
	}
	if !result.Accepted {
		return a, domain.ErrConflict
	}
	remote := ""
	if a.RemoteAccountID != nil {
		remote = *a.RemoteAccountID
	}
	if err = s.Repo.SetAccountConnection(ctx, id, "disconnected", remote, nil, nil, nil, ""); err != nil {
		return a, err
	}
	return s.Accounts.GetAccount(ctx, id)
}

func (s *Service) Claim(ctx context.Context, q *w.ClaimRequest) (*w.Lease, error) {
	return s.Coordinator.ClaimWorkflow(ctx, q, s.now())
}
func (s *Service) Renew(ctx context.Context, q *w.LeaseRequest) (*w.Permit, error) {
	return s.Coordinator.RenewWorkflow(ctx, q, s.now())
}
func (s *Service) Checkpoint(ctx context.Context, q *w.CheckpointRequest) (*w.Result, error) {
	ok, err := s.Coordinator.CheckpointWorkflow(ctx, q, s.now())
	return &w.Result{Accepted: ok}, err
}
func (s *Service) Complete(ctx context.Context, q *w.CompleteRequest) (*w.Result, error) {
	ok, err := s.Coordinator.CompleteWorkflow(ctx, q, s.now())
	return &w.Result{Accepted: ok}, err
}
func (s *Service) Fail(ctx context.Context, q *w.FailRequest) (*w.Result, error) {
	ok, err := s.Coordinator.FailWorkflow(ctx, q, s.now())
	return &w.Result{Accepted: ok}, err
}
