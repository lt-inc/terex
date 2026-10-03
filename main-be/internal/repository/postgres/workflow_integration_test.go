package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"terex/main-be/internal/domain"
	workflowservice "terex/main-be/internal/service/workflow"
	"terex/main-be/migrations"
	w "terex/proto/workflow"
)

func testSchema(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL to an isolated PostgreSQL test database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := "workflow_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := testSchema(t)
	if err := Migrate(dsn); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func fixture(t *testing.T, s *Store, targets []domain.AccountTarget) ([]domain.Account, string, string) {
	t.Helper()
	ctx := context.Background()
	accounts := []domain.Account{}
	count := len(targets)
	if count == 0 {
		count = 1
		targets = []domain.AccountTarget{{IntervalSeconds: 60, Timezone: "Asia/Tomsk"}}
	}
	for i := 0; i < count; i++ {
		a, err := s.CreateAccount(ctx, domain.Account{Platform: "youtube", Name: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetAccountConnection(ctx, a.ID, "connected", "UC-test", json.RawMessage(`{"bucket":"oauth","object":"account.json"}`), []string{"youtube.force-ssl"}, nil, ""); err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, a)
		targets[i].AccountID = a.ID
		if targets[i].Timezone == "" {
			targets[i].Timezone = "Asia/Tomsk"
		}
	}
	jid := uuid.NewString()
	sjid := jid + "-sub"
	ids := []int64{}
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	if err := s.CreateJob(ctx, domain.NewJob{JobID: jid, YoutubeURL: "https://www.youtube.com/watch?v=test", SliceMode: "full", ApproveMode: domain.ApproveBy, MinVideoDuration: 60, SubJobs: []domain.NewSubJob{{SubJobID: sjid, AccountIDs: ids, Targets: targets}}}); err != nil {
		t.Fatal(err)
	}
	clipid := jid + "-clip"
	if err := s.SaveClips(ctx, jid, []domain.Clip{{ClipID: clipid, Bucket: "clips", Object: "original.mp4", Idx: 1, StartSec: 0, EndSec: 20, Title: "Original", Hashtags: []string{}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRevision(ctx, clipid); err != nil {
		t.Fatal(err)
	}
	return accounts, sjid, clipid
}
func pubs(t *testing.T, s *Store) []domain.PublicationRecord {
	t.Helper()
	ps, err := s.ListPublicationRecords(context.Background(), domain.PublicationFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return ps
}
func nextCommand(t *testing.T, s *Store, now time.Time, kind string) w.Command {
	t.Helper()
	if err := s.PreparePublications(context.Background(), now, 50); err != nil {
		t.Fatal(err)
	}
	out, err := s.PendingOutbox(context.Background(), now, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Kind == kind {
			var cmd w.Command
			if err = json.Unmarshal(out[i].Payload, &cmd); err != nil {
				t.Fatal(err)
			}
			return cmd
		}
	}
	t.Fatal("no workflow command")
	return w.Command{}
}

func TestWorkflowMigrationQuarantinesEveryLegacyPublication(t *testing.T) {
	ctx := context.Background()
	dsn := testSchema(t)
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	if err = goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(db, ".", 1); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO accounts(id,platform,name)VALUES(1,'youtube','legacy');
      INSERT INTO jobs(job_id,youtube_url,is_vertical,is_subtitles)VALUES('legacy-job','url',TRUE,TRUE);
      INSERT INTO sub_jobs(sub_job_id,job_id,account_ids,publish_step)VALUES('legacy-sub','legacy-job',ARRAY[1::bigint],15);
      INSERT INTO clips(clip_id,job_id,bucket,object,title)VALUES('legacy-clip','legacy-job','clips','legacy.mp4','Legacy title');
      INSERT INTO publications(clip_id,sub_job_id,account_id,platform,publish_at,status,upload_id)VALUES('legacy-clip','legacy-sub',1,'youtube',now(),'published','stub-legacy');`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(dsn); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows := pubs(t, s)
	if len(rows) != 1 || rows[0].State != "needs_attention" || rows[0].Title != "Legacy title" {
		t.Fatalf("unsafe legacy migration: %+v", rows)
	}
	if rows[0].UploadID != nil || rows[0].PublishedAt != nil {
		t.Fatal("stub result retained as a real YouTube completion")
	}
	if err = s.PreparePublications(ctx, time.Now().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	out, err := s.PendingOutbox(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || len(out) != 0 {
		t.Fatalf("legacy backlog dispatched: %v %v", out, err)
	}
	if err = s.SetAccountConnection(ctx, 1, "connected", "UC-test", json.RawMessage(`{"bucket":"credentials","object":"youtube/1.json"}`), []string{"youtube.force-ssl"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ActPublication(ctx, rows[0].ID, "approve", rows[0].Version, time.Now()); err != nil {
		t.Fatal(err)
	}
	cmd := nextCommand(t, s, time.Now().Add(time.Second), "publish")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "legacy-review"}, time.Now().Add(time.Second))
	if err != nil || !lease.Granted || lease.Publication.Checkpoint.RemoteID != "" {
		t.Fatalf("reviewed stub cannot begin a real upload: %+v %v", lease, err)
	}
}

func TestWorkflowAutoApproveNeverApprovesManualRevision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	now := time.Now().UTC()
	service := &workflowservice.Service{Repo: s, Now: func() time.Time { return now }}
	if err := service.AutoApprove(ctx, clip); err != nil {
		t.Fatal(err)
	}
	initial, err := s.LatestRevision(ctx, clip)
	if err != nil || initial.ApprovedAt == nil {
		t.Fatal(initial, err)
	}
	edit := initial.EditSpec
	edit.Title = "Manual edit"
	edit.Hashtags = []string{}
	if _, err = s.SaveRevision(ctx, clip, domain.EditRequest{Version: 1, EditSpec: edit}, initial.Manifest, false); err != nil {
		t.Fatal(err)
	}
	if err = service.AutoApprove(ctx, clip); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestRevision(ctx, clip)
	if err != nil || latest.ApprovedAt != nil || latest.Version != 2 {
		t.Fatalf("pipeline duplicate approved a manual edit: %+v %v", latest, err)
	}
	if pubs(t, s)[0].State != "on_hold" {
		t.Fatal("manual edit resumed without approval")
	}
}

type connectionGateway struct {
	w.Gateway
	connection            *w.Connection
	exchangeErr, checkErr error
	expectedRemote        string
	disconnects           int
}

func (g *connectionGateway) OAuthExchange(_ context.Context, q *w.OAuthExchangeRequest) (*w.Connection, error) {
	g.expectedRemote = q.ExpectedRemoteID
	return g.connection, g.exchangeErr
}
func (g *connectionGateway) CheckConnection(context.Context, *w.ConnectionRequest) (*w.Connection, error) {
	return g.connection, g.checkErr
}
func (g *connectionGateway) Disconnect(context.Context, *w.ConnectionRequest) (*w.Result, error) {
	g.disconnects++
	return &w.Result{Accepted: true}, nil
}

func TestWorkflowConnectionFailuresPreserveBindingAndCredentials(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	accounts, _, _ := fixture(t, s, nil)
	id := accounts[0].ID
	now := time.Now().UTC()
	refBefore, err := s.CredentialRef(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &connectionGateway{exchangeErr: errors.New("temporary exchange failure")}
	service := &workflowservice.Service{Repo: s, Accounts: s, Gateway: gateway, Now: func() time.Time { return now }}
	state := uuid.NewString()
	if err = s.PutOAuthState(ctx, state, id, "https://app.test/callback", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.OAuthCallback(ctx, state, "code"); err == nil {
		t.Fatal("exchange failure hidden")
	}
	a, err := s.GetAccount(ctx, id)
	if err != nil || a.RemoteAccountID == nil || *a.RemoteAccountID != "UC-test" || gateway.expectedRemote != "UC-test" {
		t.Fatal(a, err)
	}
	refAfter, err := s.CredentialRef(ctx, id)
	if err != nil || string(refBefore) != string(refAfter) {
		t.Fatal("reconnect failure erased credentials", err)
	}
	if _, err = service.OAuthCallback(ctx, state, "code"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("OAuth state reused", err)
	}
	if err = s.SetAccountConnection(ctx, id, "connected", "UC-test", refBefore, a.ConnectionScopes, nil, ""); err != nil {
		t.Fatal(err)
	}
	gateway.checkErr = errors.New("coordinator network unavailable")
	if _, err = service.CheckConnection(ctx, id); err == nil {
		t.Fatal("network failure hidden")
	}
	a, err = s.GetAccount(ctx, id)
	if err != nil || a.ConnectionState != "connected" {
		t.Fatal("network error invalidated OAuth", a, err)
	}
	if _, err = service.Disconnect(ctx, id, a.ConnectionVersion-1); !errors.Is(err, domain.ErrConflict) || gateway.disconnects != 0 {
		t.Fatal("stale disconnect invoked external revoke", err)
	}
	if _, err = service.Disconnect(ctx, id, a.ConnectionVersion); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetAccount(ctx, id)
	if err != nil || a.ConnectionState != "disconnected" || a.RemoteAccountID == nil || *a.RemoteAccountID != "UC-test" {
		t.Fatal("disconnect lost channel binding", a, err)
	}
}

func TestWorkflowConnectionConfirmationAndReconnectResumeBlockedWork(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	accounts, _, clip := fixture(t, s, nil)
	id := accounts[0].ID
	now := time.Now().UTC().Add(time.Second)
	if err := s.ApproveRevision(ctx, clip, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountConnection(ctx, id, "pending_confirmation", "UC-test", json.RawMessage(`{"bucket":"credentials","object":"youtube/1.json"}`), nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PreparePublications(ctx, now, 10); err != nil {
		t.Fatal(err)
	}
	if pubs(t, s)[0].State != "blocked" {
		t.Fatal("unconfirmed channel dispatched")
	}
	a, err := s.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmAccountConnection(ctx, id, a.ConnectionVersion, "UC-other"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("wrong channel confirmed", err)
	}
	if err = s.ConfirmAccountConnection(ctx, id, a.ConnectionVersion, "UC-test"); err != nil {
		t.Fatal(err)
	}
	if pubs(t, s)[0].State != "scheduled" {
		t.Fatal("confirmation did not resume work")
	}
	if err = s.SetAccountConnection(ctx, id, "needs_reauth", "UC-test", json.RawMessage(`{"bucket":"credentials","object":"youtube/1.json"}`), nil, nil, "invalid_grant"); err != nil {
		t.Fatal(err)
	}
	if err = s.PreparePublications(ctx, now, 10); err != nil {
		t.Fatal(err)
	}
	if pubs(t, s)[0].State != "blocked" {
		t.Fatal("invalid grant did not block work")
	}
	if err = s.SetAccountConnection(ctx, id, "connected", "UC-test", json.RawMessage(`{"bucket":"credentials","object":"youtube/1.json"}`), nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if pubs(t, s)[0].State != "scheduled" {
		t.Fatal("reconnect did not resume work")
	}
}

func TestWorkflowTargetEditSupersedesQueuedCommandAndAddsRecipients(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	accounts, sub, clip := fixture(t, s, nil)
	now := time.Now().UTC().Add(time.Second)
	if err := s.ApproveRevision(ctx, clip, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	oldCommand := nextCommand(t, s, now, "publish")
	newAccount, err := s.CreateAccount(ctx, domain.Account{Platform: "youtube", Name: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetAccountConnection(ctx, newAccount.ID, "connected", "UC-new", json.RawMessage(`{"bucket":"credentials","object":"youtube/new.json"}`), nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	version, err := s.TargetVersion(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	start := now.Add(time.Hour)
	targets := []domain.AccountTarget{{AccountID: accounts[0].ID, StartAt: &start, IntervalSeconds: 60, Timezone: "Asia/Tomsk"}, {AccountID: newAccount.ID, StartAt: &start, IntervalSeconds: 120, Timezone: "Asia/Tomsk"}}
	if err = s.ReplaceTargets(ctx, sub, version, targets); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceTargets(ctx, sub, version, targets); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale target edit accepted", err)
	}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: oldCommand, WorkerID: "old-command"}, now)
	if err != nil || lease.Granted {
		t.Fatal("superseded schedule claimed", lease, err)
	}
	ps := pubs(t, s)
	if len(ps) != 2 {
		t.Fatal("new recipient missing approved clips", ps)
	}
	for _, p := range ps {
		if p.State != "scheduled" {
			t.Fatal("rescheduled command stranded", p)
		}
	}
	cmd := nextCommand(t, s, start.Add(3*time.Minute), "publish")
	lease, err = s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "new-command"}, start.Add(3*time.Minute))
	if err != nil || !lease.Granted {
		t.Fatal("new schedule not dispatched", lease, err)
	}
}

func TestWorkflowRescheduleChangesOneAccountAndRejectsStaleVersion(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(time.Hour)
	_, _, clip := fixture(t, s, []domain.AccountTarget{{StartAt: &start, IntervalSeconds: 60}, {StartAt: &start, IntervalSeconds: 120}})
	if err := s.ApproveRevision(ctx, clip, now); err != nil {
		t.Fatal(err)
	}
	before := pubs(t, s)
	moved := start.Add(6 * time.Hour)
	patch := domain.PublicationPatch{Version: before[0].Version, PublishAt: &moved}
	if err := s.PatchPublication(ctx, before[0].ID, patch); err != nil {
		t.Fatal(err)
	}
	if err := s.PatchPublication(ctx, before[0].ID, patch); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale calendar patch accepted: %v", err)
	}
	for _, p := range pubs(t, s) {
		if p.State != "scheduled" {
			t.Fatalf("time-only move removed approval: %+v", p)
		}
		want := before[1].PublishAt
		if p.ID == before[0].ID {
			want = moved
		}
		if !p.PublishAt.Equal(want) {
			t.Fatalf("another account was rescheduled: %+v", p)
		}
	}
}

func TestWorkflowFiveTemporaryFailuresRequireAttentionAndManualRetryResetsBudget(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	now := time.Now().UTC().Add(time.Second)
	if err := s.ApproveRevision(ctx, clip, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		cmd := nextCommand(t, s, now, "publish")
		lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "retry-worker"}, now)
		if err != nil || !lease.Granted {
			t.Fatal(lease, err)
		}
		q := w.LeaseRequest{AttemptID: cmd.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: cmd.ExpectedVersion}
		ok, err := s.FailWorkflow(ctx, &w.FailRequest{LeaseRequest: q, Code: "temporary", Message: "retry test", Retryable: true}, now)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		p := pubs(t, s)[0]
		want := "retry_wait"
		if n == 4 {
			want = "needs_attention"
		}
		if p.State != want {
			t.Fatalf("failure %d: %s", n+1, p.State)
		}
		if n < 4 {
			if err = s.PreparePublications(ctx, now.Add(time.Second), 10); err != nil {
				t.Fatal(err)
			}
			if pubs(t, s)[0].State != "retry_wait" {
				t.Fatal("backoff was ignored")
			}
		}
		now = now.Add(10 * time.Minute)
	}
	p := pubs(t, s)[0]
	if err := s.ActPublication(ctx, p.ID, "retry", p.Version, now); err != nil {
		t.Fatal(err)
	}
	cmd := nextCommand(t, s, now, "publish")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "retry-worker"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(lease, err)
	}
	q := w.LeaseRequest{AttemptID: cmd.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: cmd.ExpectedVersion}
	if ok, err := s.FailWorkflow(ctx, &w.FailRequest{LeaseRequest: q, Retryable: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if pubs(t, s)[0].State != "retry_wait" {
		t.Fatal("manual retry retained an exhausted retry budget")
	}
}

func TestWorkflowUnsupportedProviderNeverDispatches(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	accounts, _, clip := fixture(t, s, nil)
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET platform='tiktok' WHERE id=$1`, accounts[0].ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.ApproveRevision(ctx, clip, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.PreparePublications(ctx, now, 10); err != nil {
		t.Fatal(err)
	}
	out, err := s.PendingOutbox(ctx, now, 10)
	if err != nil || len(out) != 0 || pubs(t, s)[0].State != "blocked" {
		t.Fatalf("unsupported provider dispatched: %v %v", out, err)
	}
}

func TestWorkflowMetadataCannotConvertFailedRenderIntoReady(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	previous, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE clip_revisions SET render_state='failed',bucket=NULL,object=NULL WHERE id=$1`, previous.ID); err != nil {
		t.Fatal(err)
	}
	edit := previous.EditSpec
	edit.Title = "Edited after failure"
	edit.Hashtags = []string{}
	r, err := s.SaveRevision(ctx, clip, domain.EditRequest{Version: 1, EditSpec: edit}, previous.Manifest, false)
	if err != nil || r.RenderState != "failed" || r.Object != "" {
		t.Fatalf("failed artifact became ready: %+v %v", r, err)
	}
	if err = s.ApproveRevisionWithVersion(ctx, clip, 2, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("missing artifact approved", err)
	}
}

func TestWorkflowCheckpointHistoryAndPinnedVisibility(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	r, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	edit := r.EditSpec
	edit.Hashtags = []string{}
	edit.Visibility = "unlisted"
	if _, err = s.SaveRevision(ctx, clip, domain.EditRequest{Version: 1, EditSpec: edit}, r.Manifest, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	if err = s.ApproveRevisionWithVersion(ctx, clip, 2, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	cmd := nextCommand(t, s, now, "publish")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "history-worker"}, now)
	if err != nil || !lease.Granted || lease.Publication.Visibility != "unlisted" {
		t.Fatal(lease, err)
	}
	q := w.LeaseRequest{AttemptID: cmd.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: cmd.ExpectedVersion}
	cp := w.Checkpoint{State: "processing", RemoteID: "remote-video", SessionURI: "secret-session", UploadCompletedAt: &now}
	if ok, err := s.CheckpointWorkflow(ctx, &w.CheckpointRequest{LeaseRequest: q, Checkpoint: cp}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	events, err := s.PublicationEvents(ctx, cmd.PublicationID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(string(e.Detail), "secret-session") {
			t.Fatal("upload session leaked into public history")
		}
		if e.Kind == "checkpoint" && e.State == "processing" && e.AttemptID != nil && *e.AttemptID == cmd.AttemptID {
			found = true
		}
	}
	if !found {
		t.Fatal("processing checkpoint missing from attempt history")
	}
	edit.Visibility = "private"
	if _, err = s.SaveRevision(ctx, clip, domain.EditRequest{Version: 2, EditSpec: edit}, r.Manifest, false); err != nil {
		t.Fatal(err)
	}
	p := pubs(t, s)[0]
	if p.Visibility != "unlisted" || p.RevisionID != lease.Publication.RevisionID {
		t.Fatal("active upload snapshot changed", p)
	}
}

func TestWorkflowPerAccountScheduleAndMetadataSaveRequiresReapproval(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	a := now.Add(time.Hour)
	b := now.Add(2 * time.Hour)
	accounts, _, clip := fixture(t, s, []domain.AccountTarget{{StartAt: &a, IntervalSeconds: 60}, {StartAt: &b, IntervalSeconds: 120}})
	if err := s.ApproveRevisionWithVersion(ctx, clip, 1, now); err != nil {
		t.Fatal(err)
	}
	rows := pubs(t, s)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	want := map[int64]time.Time{accounts[0].ID: a.Add(time.Minute), accounts[1].ID: b.Add(2 * time.Minute)}
	for _, p := range rows {
		if !p.PublishAt.Equal(want[p.AccountID]) {
			t.Fatalf("account schedule lost: %+v", p)
		}
	}
	prev, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	edit := prev.EditSpec
	edit.Title = "Edited"
	edit.Hashtags = []string{}
	next, err := s.SaveRevision(ctx, clip, domain.EditRequest{Version: 1, EditSpec: edit}, prev.Manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pubs(t, s) {
		if p.State != "on_hold" || p.RevisionID != next.ID {
			t.Fatalf("save did not hold work: %+v", p)
		}
	}
	if err = s.ApproveRevisionWithVersion(ctx, clip, 1, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale approval accepted: %v", err)
	}
	if err = s.ApproveRevisionWithVersion(ctx, clip, 2, now); err != nil {
		t.Fatal(err)
	}
	for _, p := range pubs(t, s) {
		if p.Title != "Edited" || p.State != "scheduled" {
			t.Fatalf("reapproval retained stale snapshot: %+v", p)
		}
	}
}

func TestWorkflowRelativeScheduleAnchorDoesNotDrift(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.ApproveRevision(ctx, clip, now); err != nil {
		t.Fatal(err)
	}
	original, err := s.GetClip(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	second := original
	second.ClipID = clip + "-second"
	second.Idx = 2
	if err = s.SaveClips(ctx, original.JobID, []domain.Clip{second}); err != nil {
		t.Fatal(err)
	}
	if err = s.InitializeRevision(ctx, second.ClipID); err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveRevision(ctx, second.ClipID, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, p := range pubs(t, s) {
		idx := 1
		if p.ClipID == second.ClipID {
			idx = 2
		}
		if !p.PublishAt.Equal(now.Add(time.Duration(idx) * time.Minute)) {
			t.Fatalf("anchor drift: %+v", p)
		}
	}
}

func TestWorkflowDuplicateClaimLeaseRecoveryAndStaleCompletion(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	now := time.Now().UTC().Add(time.Second)
	if err := s.ApproveRevision(ctx, clip, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	cmd := nextCommand(t, s, now, "publish")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "worker-a"}, now)
	if err != nil || !lease.Granted {
		t.Fatalf("claim: %v %+v", err, lease)
	}
	duplicate, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "worker-b"}, now)
	if err != nil || duplicate.Granted {
		t.Fatalf("duplicate claim allowed: %v %+v", err, duplicate)
	}
	req := w.LeaseRequest{AttemptID: cmd.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: cmd.ExpectedVersion}
	cp := w.Checkpoint{State: "uploading", SessionURI: "https://upload.invalid/session", ConfirmedBytes: 1024, TotalBytes: 4096, FinalRequestStarted: true}
	if ok, err := s.CheckpointWorkflow(ctx, &w.CheckpointRequest{LeaseRequest: req, Checkpoint: cp}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cmd2 := nextCommand(t, s, now.Add(3*time.Minute), "publish")
	lease2, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd2, WorkerID: "worker-b"}, now.Add(3*time.Minute))
	if err != nil || !lease2.Granted {
		t.Fatal(err, lease2)
	}
	if lease2.Publication.Checkpoint.SessionURI != cp.SessionURI || !lease2.Publication.Checkpoint.FinalRequestStarted {
		t.Fatalf("unsafe upload restart: %+v", lease2.Publication.Checkpoint)
	}
	if ok, err := s.CompleteWorkflow(ctx, &w.CompleteRequest{LeaseRequest: req, Checkpoint: w.Checkpoint{State: "published", RemoteID: "duplicate"}}, now.Add(3*time.Minute)); err != nil || ok {
		t.Fatalf("stale worker completed: %v %v", ok, err)
	}
}

func TestWorkflowCancellationFencesActiveWorkerAndPreservesApprovedRevision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	now := time.Now().UTC().Add(time.Second)
	if err := s.ApproveRevision(ctx, clip, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	cmd := nextCommand(t, s, now, "publish")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "worker"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(err, lease)
	}
	r, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RejectRevision(ctx, clip); err != nil {
		t.Fatal(err)
	}
	after, err := s.RevisionByID(ctx, r.ID)
	if err != nil || after.ApprovedAt == nil {
		t.Fatalf("approved immutable snapshot changed: %v %+v", err, after)
	}
	permit, err := s.RenewWorkflow(ctx, &w.LeaseRequest{AttemptID: cmd.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: cmd.ExpectedVersion}, now)
	if err != nil || permit.Allowed || permit.CanPublish {
		t.Fatalf("cancelled attempt retained permission: %+v %v", permit, err)
	}
	if got := pubs(t, s)[0].State; got != "cancel_pending" {
		t.Fatal(got)
	}
}

func TestWorkflowRenderCompletionCannotPromoteStaleRevision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, _, clip := fixture(t, s, nil)
	prev, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	source := w.SourceManifest{Original: w.ObjectRef{Bucket: "downloads", Object: "source.mp4"}, DurationSec: 60}
	raw, _ := json.Marshal(source)
	edit := prev.EditSpec
	edit.EndSec = 25
	revision2, err := s.SaveRevision(ctx, clip, domain.EditRequest{Version: 1, EditSpec: edit}, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cmd := nextCommand(t, s, now, "render_revision")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "renderer"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(err, lease)
	}
	edit.Title = "Revision three"
	revision3, err := s.SaveRevision(ctx, clip, domain.EditRequest{Version: 2, EditSpec: edit}, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.CompleteWorkflow(ctx, &w.CompleteRequest{LeaseRequest: w.LeaseRequest{AttemptID: cmd.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: cmd.ExpectedVersion}, Media: lease.Render.Output}, now)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	latest, err := s.LatestRevision(ctx, clip)
	if err != nil || latest.ID != revision3.ID || latest.RenderState != "pending" {
		t.Fatalf("stale completion promoted latest: %+v %v", latest, err)
	}
	old, err := s.RevisionByID(ctx, revision2.ID)
	if err != nil || old.RenderState != "ready" {
		t.Fatal(err, old)
	}
	if !strings.Contains(old.Object, cmd.AttemptID) {
		t.Fatalf("render object not isolated per attempt: %s", old.Object)
	}
}
