package postgres

import (
	"context"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func (s *Store) FlowDiagnostics(ctx context.Context, flow string, settings twitch.Settings, now time.Time) (domain.FlowDiagnostics, error) {
	out := domain.FlowDiagnostics{Available: true, DailyLimit: settings.DailyDownloads, Timezone: settings.Timezone, CompilationBudgetPercent: settings.CompilationBudgetPercent, AdmissionMode: settings.AdmissionMode, ByFlow: []domain.DiagnosticBudgetFlow{}, Clips: []domain.ClipStatus{}}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return out, err
	}
	local := now.In(loc)
	since := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(NULLIF(t.flow_id,''),'legacy'),COALESCE(f.config->>'name','Прежний поток'),COALESCE(NULLIF(t.reservation->>'preparation_format',''),'shared'),count(*)::integer FROM twitch_collector.clips c LEFT JOIN twitch_collector.download_tasks t ON t.clip_id=c.clip_id LEFT JOIN twitch_collector.flows f ON f.id=t.flow_id WHERE c.reserved_at >= $1 AND(c.error_kind<>'capacity' OR COALESCE(t.reservation->>'import_id','')<>'') GROUP BY t.flow_id,f.config->>'name',COALESCE(NULLIF(t.reservation->>'preparation_format',''),'shared') ORDER BY count(*) DESC,t.flow_id`, since)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.DiagnosticBudgetFlow
		if err = rows.Scan(&v.FlowID, &v.Name, &v.Format, &v.Spent); err != nil {
			rows.Close()
			return out, err
		}
		out.Spent += v.Spent
		out.ByFlow = append(out.ByFlow, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Remaining = max(0, out.DailyLimit-out.Spent)
	rows, err = s.pool.Query(ctx, `SELECT c.clip_id,`+dispatchVisibleState+`,`+dispatchVisibleReason+` FROM twitch_collector.clips c JOIN twitch_collector.consumers co ON co.clip_id=c.clip_id LEFT JOIN twitch_collector.download_tasks t ON t.clip_id=c.clip_id WHERE co.flow_id=$1 AND NOT co.excluded AND c.state NOT IN('imported','duplicate') ORDER BY c.created_at DESC LIMIT 10001`, flow)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.ClipStatus
		if err = rows.Scan(&v.ClipID, &v.State, &v.Reason); err != nil {
			rows.Close()
			return out, err
		}
		out.Clips = append(out.Clips, v)
	}
	rows.Close()
	return out, rows.Err()
}
