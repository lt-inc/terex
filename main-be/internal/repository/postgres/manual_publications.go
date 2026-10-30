package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/publicationmetadata"
	"terex/proto/workflow"
)

type manualMedia struct {
	ref      workflow.ObjectRef
	sha      string
	size     int64
	duration float64
}

// Only server-owned, verified references may become publication media.
func manualMediaTx(ctx context.Context, tx pgx.Tx, r domain.Revision, kind string) (manualMedia, error) {
	var m manualMedia
	if err := storageRevisionAvailable(ctx, tx, r.ID); err != nil {
		return m, err
	}
	if err := twitchResetClipAllowed(ctx, tx, r.ClipID); err != nil {
		return m, err
	}
	if err := requireTwitchSources(ctx, tx, r.ClipID, r.ID); err != nil {
		return m, err
	}
	if err := requireFlowFiltersTx(ctx, tx, r.ClipID, r.ID); err != nil {
		return m, err
	}
	var rejected bool
	if err := tx.QueryRow(ctx, `SELECT approval='rejected' FROM clips WHERE clip_id=$1`, r.ClipID).Scan(&rejected); err != nil {
		return m, err
	}
	if rejected {
		return m, fmt.Errorf("%w: Клип отклонён; сначала верните его в доступные", domain.ErrConflict)
	}
	if kind == "rendered" {
		if r.RenderState != "ready" || r.Bucket == "" || r.Object == "" {
			return m, fmt.Errorf("%w: Готовый клип ещё не подготовлен: выберите проверенный скачанный оригинал или дождитесь рендера", domain.ErrConflict)
		}
		m.ref = workflow.ObjectRef{Bucket: r.Bucket, Object: r.Object}
		err := tx.QueryRow(ctx, `SELECT media_sha256,media_size_bytes FROM clip_revisions WHERE id=$1`, r.ID).Scan(&m.sha, &m.size)
		return m, err
	}
	var source workflow.SourceManifest
	if json.Unmarshal(r.Manifest, &source) != nil || source.Assembly != nil || source.Original.Object == "" {
		return m, fmt.Errorf("%w: Нет проверенного оригинала для выпуска: сначала подготовьте эту версию в редакторе", domain.ErrConflict)
	}
	var raw []byte
	// Flow imports may point to a shared immutable media record instead of
	// carrying verified bytes on the alias import itself.
	err := tx.QueryRow(ctx, `SELECT COALESCE(m.verified,i.verified) FROM twitch_imports i LEFT JOIN twitch_media m ON m.id=i.media_id
 WHERE (i.clip_id=$1 OR i.id IN(SELECT source_id FROM twitch_outputs WHERE clip_id=$1)) AND NOT i.rejected AND i.media_state='ready'
 AND (m.id IS NULL OR m.storage_state='available') LIMIT 1`, r.ClipID).Scan(&raw)
	if err != nil && err != pgx.ErrNoRows {
		return m, err
	}
	var v workflow.VerifiedMedia
	if err != nil || json.Unmarshal(raw, &v) != nil || v.Original != source.Original || !validMediaSHA(v.SHA256) || v.SizeBytes <= 0 || v.DurationSec <= 0 {
		return m, fmt.Errorf("%w: У оригинала нет подтверждённого SHA файла: подготовьте новый рендер в редакторе", domain.ErrConflict)
	}
	m = manualMedia{ref: v.Original, sha: v.SHA256, size: v.SizeBytes, duration: v.DurationSec}
	return m, storageRefsAvailable(ctx, tx, m.ref)
}

func validMediaSHA(s string) bool { b, err := hex.DecodeString(s); return err == nil && len(b) == 32 }

func (s *Store) ClipPublicationOptions(ctx context.Context, clip string) (domain.ClipPublicationOptions, error) {
	out := domain.ClipPublicationOptions{Variants: []domain.PublicationVariant{}, Publications: []domain.PublicationRecord{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	// Share the same snapshot/GC/reset lock as submission. No mutation or remote
	// request is needed just to explain eligibility.
	if err = lockTwitch(ctx, tx); err != nil {
		return out, err
	}
	r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=(SELECT latest_revision_id FROM clips WHERE clip_id=$1)`, clip))
	if err != nil {
		return out, err
	}
	out.RevisionID, out.Version, out.Title = r.ID, r.Version, r.Title
	out.Caption = tikTokCaption(r.Title, r.Description, r.Hashtags)
	for _, kind := range []string{"rendered", "source"} {
		v := domain.PublicationVariant{MediaKind: kind, Eligible: true, Platforms: []string{"youtube", "tiktok"}}
		if media, e := manualMediaTx(ctx, tx, r, kind); e != nil {
			if !errors.Is(e, domain.ErrConflict) && !errors.Is(e, domain.ErrInvalid) {
				return out, e
			}
			v.Eligible, v.Reason = false, strings.TrimPrefix(strings.TrimPrefix(e.Error(), domain.ErrConflict.Error()+": "), domain.ErrInvalid.Error()+": ")
			v.Platforms = []string{}
		} else if !validMediaSHA(media.sha) || media.size <= 0 {
			v.Platforms = []string{"youtube"}
			v.Reason = "Доступно только для YouTube: TikTok требует подтверждённые SHA и размер. Измените медиа в редакторе и сохраните новый рендер либо выберите проверенный скачанный оригинал."
		}
		out.Variants = append(out.Variants, v)
	}
	rows, err := tx.Query(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.clip_id=$1 ORDER BY p.id DESC LIMIT 100`, clip)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		p, e := scanPublication(rows)
		if e != nil {
			return out, e
		}
		out.Publications = append(out.Publications, p)
	}
	return out, rows.Err()
}

func manualFingerprint(clip string, u domain.ManualPublishRequest) string {
	u.RequestID = ""
	hash := sha256.Sum256(append([]byte(clip+"\n"), mustJSON(u)...))
	return hex.EncodeToString(hash[:])
}

func (s *Store) CreateManualPublication(ctx context.Context, clip string, u domain.ManualPublishRequest, now time.Time) (domain.PublicationRecord, error) {
	var out domain.PublicationRecord
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return out, err
	}
	fingerprint := manualFingerprint(clip, u)
	var previous string
	var pid int64
	err = tx.QueryRow(ctx, `SELECT fingerprint,publication_id FROM manual_publication_requests WHERE request_id=$1::uuid`, u.RequestID).Scan(&previous, &pid)
	if err == nil {
		if previous != fingerprint {
			return out, fmt.Errorf("%w: Этот запрос уже использован с другими параметрами: откройте существующую публикацию", domain.ErrConflict)
		}
		return scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1`, pid))
	}
	if err != pgx.ErrNoRows {
		return out, err
	}
	// Fresh UUIDs must not restart failed/uncertain work or duplicate an existing
	// scheduled target. A retry/reconciliation uses the original publication.
	err = tx.QueryRow(ctx, `SELECT p.id,COALESCE((SELECT q.fingerprint FROM manual_publication_requests q WHERE q.publication_id=p.id LIMIT 1),'') FROM publications p JOIN accounts existing ON existing.id=p.account_id JOIN accounts target ON target.id=$2 WHERE p.clip_id=$1 AND (p.account_id=$2 OR (existing.platform=target.platform AND COALESCE(existing.remote_account_id,'')<>'' AND existing.remote_account_id=target.remote_account_id)) ORDER BY p.id LIMIT 1`, clip, u.AccountID).Scan(&pid, &previous)
	if err == nil {
		if previous != fingerprint {
			return out, fmt.Errorf("%w: Для клипа и аккаунта уже есть публикация №%d: проверьте её состояние или выполните сверку перед повтором", domain.ErrConflict, pid)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO manual_publication_requests(request_id,clip_id,fingerprint,publication_id)VALUES($1::uuid,$2,$3,$4)`, u.RequestID, clip, fingerprint, pid); err != nil {
			return out, err
		}
		out, err = scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1`, pid))
		if err != nil {
			return out, err
		}
		return out, tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return out, err
	}
	var rid int64
	if err = tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1 FOR UPDATE`, clip).Scan(&rid); err != nil {
		return out, mapErr(err)
	}
	r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, rid))
	if err != nil {
		return out, err
	}
	if r.ID != u.RevisionID || r.Version != u.Version {
		return out, domain.ErrConflict
	}
	media, err := manualMediaTx(ctx, tx, r, u.MediaKind)
	if err != nil {
		return out, err
	}
	var platform, connection, remote string
	var credential []byte
	var session int64
	if err = tx.QueryRow(ctx, `SELECT platform,connection_state,COALESCE(remote_account_id,''),credential_ref,session_version FROM accounts WHERE id=$1 FOR UPDATE`, u.AccountID).Scan(&platform, &connection, &remote, &credential, &session); err != nil {
		return out, mapErr(err)
	}
	var ref workflow.ObjectRef
	_ = json.Unmarshal(credential, &ref)
	if platform != u.Platform {
		return out, fmt.Errorf("%w: Аккаунт относится к другой платформе", domain.ErrInvalid)
	}
	if connection != "connected" || remote == "" || ref.Bucket == "" || ref.Object == "" || (platform == "tiktok" && (session <= 0 || ref.Bucket != "tiktok-sessions")) {
		return out, fmt.Errorf("%w: Аккаунт нужно подключить и подтвердить перед публикацией", domain.ErrConflict)
	}
	if platform == "tiktok" && (!validMediaSHA(media.sha) || media.size <= 0) {
		return out, fmt.Errorf("%w: Для TikTok нужны подтверждённые SHA и размер: выберите проверенный оригинал или сохраните новый рендер", domain.ErrConflict)
	}
	if platform == "youtube" {
		if err = publicationmetadata.Validate(r.Title, publicationmetadata.Compose(u.Caption, nil)); err != nil {
			return out, fmt.Errorf("%w: %s", domain.ErrInvalid, err)
		}
	}
	// Clone without changing latest_revision_id, editor approval, flow targets or
	// pending render attempts. Approval belongs only to this explicit click.
	spec := r.EditSpec
	if u.MediaKind == "source" {
		spec.StartSec, spec.EndSec, spec.SubtitlesEnabled, spec.Cues = 0, media.duration, false, []domain.Cue{}
	}
	spec.Visibility = u.Visibility
	var snapshot int64
	if err = tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,bucket,object,manifest,approved_at,media_sha256,media_size_bytes,publication_only) VALUES($1,$2,$3,'ready',$4,$5,$6,$7,$8,$9,TRUE)RETURNING id`, clip, r.Version, mustJSON(spec), media.ref.Bucket, media.ref.Object, r.Manifest, now, media.sha, media.size).Scan(&snapshot); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilation_items(compilation_revision_id,source_revision_id,source_clip_id,ordinal,source_id,media_id) SELECT $1,source_revision_id,source_clip_id,ordinal,source_id,media_id FROM twitch_compilation_items WHERE compilation_revision_id=$2`, snapshot, rid); err != nil {
		return out, err
	}
	title := strings.TrimSpace(strings.SplitN(u.Caption, "\n", 2)[0])
	// YouTube's title limit is separate from its description. Keep the reviewed
	// revision title; the user-supplied caption becomes the pinned description.
	if platform == "youtube" {
		title = r.Title
	}
	metadata := domain.PublicationMetadata{Title: title, Description: u.Caption, Caption: u.Caption, CaptionManual: true, Visibility: u.Visibility, Hashtags: []string{}}
	key := remote
	if platform != "youtube" {
		key = platform + ":" + remote
	}
	if err = tx.QueryRow(ctx, `INSERT INTO publications(clip_id,account_id,platform,publish_at,next_attempt_at,revision_id,metadata,status,timezone,destination_key,manual,manual_source_revision_id,manual_media_kind) VALUES($1,$2,$3,$4,$4,$5,$6,'scheduled','Asia/Tomsk',$7,TRUE,$8,$9)RETURNING id`, clip, u.AccountID, platform, now, snapshot, mustJSON(metadata), key, rid, u.MediaKind).Scan(&pid); err != nil {
		return out, err
	}
	if u.FreshnessOverride {
		if err = setPublicationOverrideTx(ctx, tx, pid, true, now); err != nil {
			return out, err
		}
	}
	if allowed, e := enforceUnstartedPolicyTx(ctx, tx, pid, now); e != nil {
		return out, e
	} else if !allowed {
		return out, fmt.Errorf("%w: Выпуск удержан правилами источника: проверьте доступность и фильтры; для устаревшего Twitch-клипа явно подтвердите исключение актуальности", domain.ErrConflict)
	}
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return out, err
	}
	if allowed, e := twitchRuntimeCapacity(ctx, tx, pid, now); e != nil {
		return out, e
	} else if !allowed {
		return out, fmt.Errorf("%w: Дневная квота загрузок исчерпана", domain.ErrConflict)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO manual_publication_requests(request_id,clip_id,fingerprint,publication_id)VALUES($1::uuid,$2,$3,$4)`, u.RequestID, clip, fingerprint, pid); err != nil {
		return out, err
	}
	if err = insertEvent(ctx, tx, pid, "", "manual_requested", "scheduled", map[string]any{"reviewed_revision_id": rid, "publication_revision_id": snapshot, "media_kind": u.MediaKind, "visibility": u.Visibility}); err != nil {
		return out, err
	}
	out, err = scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1`, pid))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
