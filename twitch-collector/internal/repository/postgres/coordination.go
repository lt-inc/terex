package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

const coordinationMigration = `
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS dispatch_state text NOT NULL DEFAULT 'waiting_assignment';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS grant_checked_at timestamptz;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS grant_expires_at timestamptz;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS verification_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS dispatch_reason_code text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS dispatch_reason text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS transfer_phase text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS dispatch_entered_at timestamptz;
UPDATE twitch_collector.download_tasks SET dispatch_entered_at=COALESCE(started_at,updated_at,run_at) WHERE dispatch_entered_at IS NULL;
ALTER TABLE twitch_collector.download_tasks ALTER COLUMN dispatch_entered_at SET DEFAULT now();
ALTER TABLE twitch_collector.download_tasks ALTER COLUMN dispatch_entered_at SET NOT NULL;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM twitch_collector.schema_version WHERE version=5) THEN
  UPDATE twitch_collector.download_tasks SET dispatch_state='verification_pending'
  WHERE(state='queued' OR(state='leased' AND lease_expires_at<=now())) AND (uploaded IS NOT NULL OR(COALESCE((reservation->>'upload_generation')::bigint,0)>0 AND error_code<>'upload_missing' AND transfer_phase<>'reserved'));
  INSERT INTO twitch_collector.schema_version(version) VALUES(5);
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS download_dispatch_ready ON twitch_collector.download_tasks(dispatch_state,run_at,clip_id) WHERE state IN('queued','leased');
`

// QueueCompletion is the durable handoff. Releasing the download lease and
// publishing verification work are one transaction, including uncertain PUTs.
func (s *Store) QueueCompletion(ctx context.Context, t domain.Task, media domain.UploadedMedia) error {
	raw, err := json.Marshal(media)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET uploaded=$3,transfer_phase=$4,dispatch_state='verification_pending',dispatch_entered_at=now(),dispatch_reason_code='',dispatch_reason='',state='queued',run_at=now(),last_error='',error_code='',lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE `+validLease, t.Clip.ID, t.Token, raw, media.Phase)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.clips SET state='queued',last_error='',error_kind='' WHERE clip_id=$1`, t.Clip.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ClaimDownload(ctx context.Context, lease time.Duration) (*domain.Task, error) {
	return s.claimLane(ctx, lease, false, true)
}
func (s *Store) ClaimExistingDownload(ctx context.Context, lease time.Duration) (*domain.Task, error) {
	return s.claimLane(ctx, lease, false, false)
}
func (s *Store) ClaimCompletion(ctx context.Context, lease time.Duration) (*domain.Task, error) {
	return s.claimLane(ctx, lease, true, true)
}
func (s *Store) claimLane(ctx context.Context, lease time.Duration, completion, allowNewPhysical bool) (*domain.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// An interrupted PUT retains its exact generation. It is never redownloaded
	// before the completion lane checks the uploaded object's actual state.
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.download_tasks SET dispatch_state='verification_pending' WHERE state IN('queued','leased') AND dispatch_state<>'verification_pending' AND (uploaded IS NOT NULL OR(COALESCE((reservation->>'upload_generation')::bigint,0)>0 AND error_code<>'upload_missing' AND transfer_phase<>'reserved')) AND (state='queued' OR lease_expires_at<=now())`); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `WITH expired AS(UPDATE twitch_collector.download_tasks SET state='failed',last_error='Worker lease expired after the retry budget',error_code='worker_lease_expired',failure_token=md5(random()::text||clip_id||now()::text),failure_synced_at=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
 WHERE state='leased' AND lease_expires_at<=now() AND dispatch_state<>'verification_pending' AND attempts>=3 RETURNING clip_id)
 UPDATE twitch_collector.clips SET state='failed',last_error='Worker lease expired after the retry budget',error_kind='internal' WHERE clip_id IN(SELECT clip_id FROM expired)`); err != nil {
		return nil, err
	}
	token := newID()
	var id, flowID, dispatch, errorCode, transferPhase string
	var attempt, flowVersion int
	var epoch int64
	var reservation, uploaded []byte
	err = tx.QueryRow(ctx, `WITH candidate AS (
 SELECT t.clip_id FROM twitch_collector.download_tasks t JOIN twitch_collector.clips c ON c.clip_id=t.clip_id JOIN twitch_collector.channels ch ON ch.id=c.channel_id
 WHERE t.epoch=(SELECT epoch FROM twitch_collector.state WHERE id=1)
 AND ((t.state='queued' AND t.run_at<=now()) OR(t.state='leased' AND t.lease_expires_at<=now()))
 AND CASE WHEN $3 THEN t.dispatch_state='verification_pending'
 ELSE t.dispatch_state='authorized' AND t.attempts<3 AND t.uploaded IS NULL AND(COALESCE((t.reservation->>'upload_generation')::bigint,0)=0 OR t.error_code='upload_missing' OR t.transfer_phase='reserved')
 AND NOT ch.paused AND NOT ch.archived
 AND (COALESCE((t.reservation->>'admission_sequence')::bigint,0)=0 OR(t.grant_checked_at>now()-interval '30 seconds' AND (t.error_code='upload_missing' OR t.transfer_phase='reserved' OR t.grant_expires_at IS NULL OR t.grant_expires_at>now())))
 AND(t.flow_id='' OR EXISTS(SELECT 1 FROM twitch_collector.consumers co JOIN twitch_collector.flows f ON f.id=co.flow_id WHERE co.clip_id=t.clip_id AND co.eligible AND NOT co.excluded AND f.enabled)) END
 AND($4 OR(COALESCE((t.reservation->>'upload_generation')::bigint,0)>0 AND COALESCE(t.reservation->>'reservation_id','')<>''))
 ORDER BY t.run_at,t.clip_id FOR UPDATE OF t SKIP LOCKED LIMIT 1)
 UPDATE twitch_collector.download_tasks t SET state='leased',attempts=attempts+CASE WHEN $3 THEN 0 ELSE 1 END,verification_attempts=verification_attempts+CASE WHEN $3 THEN 1 ELSE 0 END,
 lease_token=$1,lease_expires_at=now()+$2*interval '1 second',updated_at=now(),started_at=now() FROM candidate WHERE t.clip_id=candidate.clip_id
 RETURNING t.clip_id,CASE WHEN $3 THEN t.verification_attempts ELSE t.attempts END,t.flow_id,t.flow_version,t.reservation,t.epoch,t.uploaded,t.dispatch_state,t.error_code,t.transfer_phase`, token, lease.Seconds(), completion, allowNewPhysical).Scan(&id, &attempt, &flowID, &flowVersion, &reservation, &epoch, &uploaded, &dispatch, &errorCode, &transferPhase)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	stage := "downloading"
	if completion {
		stage = "verifying"
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.clips SET state=$2 WHERE clip_id=$1`, id, stage); err != nil {
		return nil, err
	}
	c, err := scanClip(tx.QueryRow(ctx, "SELECT "+clipColumns+" FROM twitch_collector.clips c WHERE c.clip_id=$1", id))
	if err != nil {
		return nil, err
	}
	task := &domain.Task{Epoch: epoch, Clip: c, Token: token, Attempt: attempt, FlowID: flowID, FlowVersion: flowVersion, Completion: completion, DispatchState: dispatch, ErrorCode: errorCode, TransferPhase: transferPhase}
	if len(uploaded) > 0 && string(uploaded) != "null" {
		if err = json.Unmarshal(uploaded, &task.Uploaded); err != nil {
			return nil, err
		}
	}
	if len(reservation) > 0 {
		if err = json.Unmarshal(reservation, &task.Reservation); err != nil {
			return nil, err
		}
	}
	return task, tx.Commit(ctx)
}

// GrantRefs pages all waiting/authorized tasks. A bounded admissions response
// cannot tell us whether an omitted grant is still valid.
func (s *Store) GrantRefs(ctx context.Context, after string, limit int) ([]twitch.PressureTaskRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT clip_id,COALESCE(reservation->>'import_id',''),flow_id,COALESCE((reservation->>'admission_sequence')::bigint,0),COALESCE((reservation->>'upload_generation')::bigint,0)
 FROM twitch_collector.download_tasks WHERE (state='queued' OR(state='leased' AND lease_expires_at<=now())) AND flow_id<>'' AND COALESCE(reservation->>'import_id','')<>'' AND dispatch_state IN('waiting_assignment','authorized') AND clip_id>$1 ORDER BY clip_id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []twitch.PressureTaskRef{}
	for rows.Next() {
		var r twitch.PressureTaskRef
		if err = rows.Scan(&r.ClipID, &r.ImportID, &r.FlowID, &r.AdmissionSequence, &r.UploadGeneration); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ApplyPressure(ctx context.Context, result twitch.PressureResult) error {
	if result.Maintenance {
		return domain.ErrPaused
	}
	for _, r := range result.Tasks {
		if r.GrantState == "consumed" && r.UploadGeneration > 0 {
			// Older collectors could lose ReserveImport's response before
			// persisting the generation. A consumed grant is not a new download
			// permission, even when its original expiry is in the past. Retain
			// the exact server generation and let completion inspect it; neither
			// this response nor a generation proves that a PUT took place.
			tag, err := s.pool.Exec(ctx, `WITH reconciled AS (
 UPDATE twitch_collector.download_tasks SET reservation=COALESCE(reservation,'{}'::jsonb)||jsonb_build_object('upload_generation',$6::bigint),
 state='queued',dispatch_state='verification_pending',dispatch_entered_at=now(),run_at=now(),transfer_phase='reconciling',
 grant_checked_at=$7,grant_expires_at=$8,dispatch_reason_code='upload_reconciliation_pending',dispatch_reason=$9,
 lease_token=NULL,lease_expires_at=NULL,updated_at=now()
 WHERE clip_id=$1 AND epoch=$2 AND epoch=(SELECT epoch FROM twitch_collector.state WHERE id=1)
 AND flow_id=$3 AND COALESCE(reservation->>'import_id','')=$4 AND COALESCE((reservation->>'admission_sequence')::bigint,0)=$5
 AND (state='queued' OR(state='leased' AND lease_expires_at<=now())) AND dispatch_state IN('waiting_assignment','authorized')
 AND uploaded IS NULL AND COALESCE((reservation->>'upload_generation')::bigint,0)=0
 RETURNING clip_id)
 UPDATE twitch_collector.clips SET state='queued',last_error=$9,error_kind='reconciliation' WHERE clip_id IN(SELECT clip_id FROM reconciled)`, r.ClipID, result.Epoch, r.FlowID, r.ImportID, r.AdmissionSequence, r.UploadGeneration, result.ObservedAt, r.ExpiresAt, assignmentReason("upload_reconciliation_pending"))
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				continue
			}
		}
		state := "waiting_assignment"
		if r.GrantState == "valid" || r.GrantState == "consumed" {
			state = "authorized"
		}
		_, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET dispatch_entered_at=CASE WHEN dispatch_state IS DISTINCT FROM $6 THEN now() ELSE dispatch_entered_at END,dispatch_state=$6,grant_checked_at=$7,grant_expires_at=$8,dispatch_reason_code=$10,dispatch_reason=$11
 WHERE clip_id=$1 AND(state='queued' OR(state='leased' AND lease_expires_at<=now())) AND epoch=$2 AND epoch=(SELECT epoch FROM twitch_collector.state WHERE id=1) AND flow_id=$3 AND COALESCE(reservation->>'import_id','')=$4 AND COALESCE((reservation->>'admission_sequence')::bigint,0)=$5
 AND uploaded IS NULL AND(COALESCE((reservation->>'upload_generation')::bigint,0)=0 OR((error_code='upload_missing' OR transfer_phase='reserved') AND COALESCE((reservation->>'upload_generation')::bigint,0)=$9))
 AND(dispatch_state IS DISTINCT FROM $6 OR grant_checked_at IS DISTINCT FROM $7 OR grant_expires_at IS DISTINCT FROM $8 OR dispatch_reason_code IS DISTINCT FROM $10)`, r.ClipID, result.Epoch, r.FlowID, r.ImportID, r.AdmissionSequence, state, result.ObservedAt, r.ExpiresAt, r.UploadGeneration, r.ReasonCode, assignmentReason(r.ReasonCode))
		if err != nil {
			return err
		}
	}
	return nil
}

func assignmentReason(code string) string {
	switch code {
	case "", "media_ready":
		return ""
	case "daily_download_limit":
		return "Общая дневная квота скачиваний исчерпана"
	case "broadcaster_daily_limit":
		return "Дневная квота этого Twitch-канала исчерпана"
	case "flow_paused":
		return "Поток остановлен или архивирован"
	case "assignment_expired":
		return "Срок назначения истёк; ожидается новое распределение бюджета"
	case "assignment_changed", "assignment_released":
		return "Прежнее назначение заменено или отозвано; ожидается новое"
	case "import_missing":
		return "Импорт отсутствует в Terex; ожидается новое наблюдение и назначение"
	case "upload_generation_changed":
		return "Изменилась загрузка или эпоха; прежнее задание остановлено"
	case "upload_reconciliation_pending":
		return "Проверяется точное состояние прежней загрузки; повторное скачивание не разрешено"
	case "twitch_reset":
		return "Идёт полный сброс Twitch"
	case "source_rejected_or_expired":
		return "Источник отклонён или его файл недоступен"
	case "collector_disabled":
		return "Коллектор остановлен в настройках"
	case "source_filtered":
		return "Источник больше не соответствует фильтрам или сроку актуальности потока"
	default:
		return "Ожидается назначение из общего бюджета скачиваний"
	}
}

func (s *Store) AuthorizeLegacy(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET dispatch_state='authorized' WHERE(state='queued' OR(state='leased' AND lease_expires_at<=now())) AND dispatch_state='waiting_assignment' AND uploaded IS NULL AND COALESCE((reservation->>'upload_generation')::bigint,0)=0 AND COALESCE((reservation->>'admission_sequence')::bigint,0)=0`)
	return err
}
