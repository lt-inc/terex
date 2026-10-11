package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

func freezeFlowAssembly(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, f twitch.Flow, owner string) (workflow.SourceManifest, error) {
	m := workflow.SourceManifest{Options: msg.JobOptions{Vertical: false, Subtitles: false}, Cues: []workflow.Cue{}, Assembly: &workflow.AssemblySpec{SchemaVersion: 2, Profile: "landscape_1080p30", BumperDurationFrames: 30, Items: []workflow.AssemblyItem{}}}
	if strings.TrimSpace(req.Title) == "" || len([]rune(req.Title)) > 100 || len(req.Items) < f.CompilationMinClips || len(req.Items) > f.CompilationMaxClips {
		return m, fmt.Errorf("%w: задайте название и от %d до %d исходников", domain.ErrInvalid, f.CompilationMinClips, f.CompilationMaxClips)
	}
	seen := map[string]bool{}
	for _, item := range req.Items {
		var b, vb []byte
		var id, media string
		var excluded bool
		err := tx.QueryRow(ctx, `SELECT i.id,i.request,m.id,m.verified,fs.excluded OR i.rejected FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1 AND m.storage_state='available' AND i.media_state='ready' AND(( $2<>'' AND i.id=$2)OR($2='' AND i.clip_id=$3))`, f.ID, item.SourceID, item.ClipID).Scan(&id, &b, &media, &vb, &excluded)
		if err != nil {
			return m, mapErr(err)
		}
		if excluded || seen[media] {
			return m, fmt.Errorf("%w: исходник исключён или повторяется", domain.ErrConflict)
		}
		seen[media] = true
		var used bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_source_uses WHERE youtube_channel_id=$1 AND format='compilation' AND media_id=$2 AND($3='' OR clip_id IS DISTINCT FROM $3))`, f.YouTubeChannelID, media, owner).Scan(&used); err != nil {
			return m, err
		}
		if used {
			return m, fmt.Errorf("%w: исходник уже использован в подборке этого YouTube-канала", domain.ErrConflict)
		}
		var r twitch.ImportRequest
		var v workflow.VerifiedMedia
		if json.Unmarshal(b, &r) != nil || json.Unmarshal(vb, &v) != nil {
			return m, domain.ErrInvalid
		}
		if !sourceMatches(f, r.Source) {
			return m, fmt.Errorf("%w: исходник не соответствует текущим каналам, категории или языку потока", domain.ErrConflict)
		}
		if err = storageRefsAvailable(ctx, tx, v.Original); err != nil {
			return m, err
		}
		start, end := item.StartSec, item.EndSec
		if end == 0 {
			end = v.DurationSec
		}
		if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > v.DurationSec+0.05 {
			return m, domain.ErrInvalid
		}
		m.Assembly.Items = append(m.Assembly.Items, workflow.AssemblyItem{TwitchSourceID: id, MediaID: media, Original: v.Original, SHA256: v.SHA256, StartSec: start, EndSec: end, BroadcasterLogin: r.Source.BroadcasterLogin})
		m.DurationSec += math.Ceil((end-start)*30-1e-7) / 30
	}
	m.DurationSec += float64(len(req.Items) - 1)
	if m.DurationSec > f.CompilationMaxDurationSec+0.05 {
		return m, fmt.Errorf("%w: подборка с переходами превышает максимальную длину", domain.ErrInvalid)
	}
	return m, nil
}
func reserveAssemblyUse(ctx context.Context, tx pgx.Tx, f twitch.Flow, clip string, m workflow.SourceManifest, now time.Time) error {
	for _, item := range m.Assembly.Items {
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id,created_at)VALUES($1,'compilation',$2,$3,$4)ON CONFLICT DO NOTHING`, f.YouTubeChannelID, item.MediaID, clip, now); err != nil {
			return err
		}
	}
	return nil
}
func createFlowCompilationTx(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, day *time.Time, now time.Time) (domain.Compilation, error) {
	f, err := flowByID(ctx, tx, req.FlowID)
	if err != nil {
		return domain.Compilation{}, err
	}
	if err = confirmedFlow(ctx, tx, f); err != nil {
		return domain.Compilation{}, err
	}
	if !f.CompilationsEnabled {
		return domain.Compilation{}, fmt.Errorf("%w: подборки в потоке выключены", domain.ErrConflict)
	}
	m, err := freezeFlowAssembly(ctx, tx, req, f, "")
	if err != nil {
		return domain.Compilation{}, err
	}
	ids := []string{}
	for _, item := range m.Assembly.Items {
		ids = append(ids, item.TwitchSourceID)
	}
	if _, err = reserveOutputPreparationTx(ctx, tx, f, "", "compilation", ids, now); err != nil {
		return domain.Compilation{}, err
	}
	id, jid := "compilation-"+uuid.NewString(), uuid.NewString()
	sources, err := assemblyMetadataSources(ctx, tx, m.Assembly)
	if err != nil {
		return domain.Compilation{}, err
	}
	rid, err := createTwitchDraft(ctx, tx, jid, id, "twitch_compilation", "compilation", "", "", req.Title, f.AccountID, f.Timezone, m, now, flowMetadataInput(f, "compilation", req.Title, sources))
	if err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilations(clip_id,flow_id,local_date,timezone,created_at)VALUES($1,$2,$3,$4,$5)`, id, f.ID, day, f.Timezone, now); err != nil {
		return domain.Compilation{}, err
	}
	if err = saveAssemblyItems(ctx, tx, rid, m.Assembly); err != nil {
		return domain.Compilation{}, err
	}
	if err = reserveAssemblyUse(ctx, tx, f, id, m, now); err != nil {
		return domain.Compilation{}, err
	}
	auto := f.AutoPublish && f.AutoEnabledAt != nil && !now.Before(*f.AutoEnabledAt)
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,account_id,youtube_channel_id,format,config,initial_revision_id,auto_eligible,created_at)VALUES($1,$2,$3,$4,'compilation',$5,$6,$7,$8)`, id, f.ID, f.AccountID, f.YouTubeChannelID, mustJSON(f), rid, auto, now); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET clip_id=$2 WHERE r.flow_id=$1 AND r.format='compilation' AND r.state='active' AND r.clip_id IS NULL AND NOT EXISTS(SELECT 1 FROM unnest($3::text[]) x WHERE NOT EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id AND ps.import_id=x))`, f.ID, id, ids); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET work_started_at=COALESCE(work_started_at,$2)WHERE id=$1`, f.ID, now); err != nil {
		return domain.Compilation{}, err
	}
	if err = tightenQueuedRender(ctx, tx, rid, now); err != nil {
		return domain.Compilation{}, err
	}
	return compilationByID(ctx, tx, id)
}

type flowCandidate struct {
	ID, Media, Broadcaster string
	Duration               float64
}

// Views order is preserved within each broadcaster. Balance is soft: exhausted
// broadcasters yield their time to others, and only whole clips fit the budget.
func selectFlowCandidates(pool []flowCandidate, f twitch.Flow) []twitch.CompilationItemRequest {
	out := []twitch.CompilationItemRequest{}
	spent := map[string]float64{}
	seen := map[string]bool{}
	duration := 0.0
	for len(out) < f.CompilationMaxClips {
		pick := -1
		for n, c := range pool {
			extra := math.Ceil(c.Duration*30-1e-7) / 30
			if len(out) > 0 {
				extra++
			}
			if seen[c.Media] || c.ID == "" || duration+extra > f.CompilationMaxDurationSec+0.05 {
				continue
			}
			if pick < 0 || (f.BalanceByTime && spent[c.Broadcaster] < spent[pool[pick].Broadcaster]) {
				pick = n
			}
		}
		if pick < 0 {
			break
		}
		c := pool[pick]
		seen[c.Media] = true
		spent[c.Broadcaster] += c.Duration
		if len(out) > 0 {
			duration++
		}
		duration += math.Ceil(c.Duration*30-1e-7) / 30
		out = append(out, twitch.CompilationItemRequest{SourceID: c.ID, EndSec: c.Duration})
	}
	return out
}
