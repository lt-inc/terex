package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

func flowFilterViolationTx(ctx context.Context, tx pgx.Tx, clip string, rid int64) (*domain.TwitchPolicyViolation, error) {
	var fid string
	err := tx.QueryRow(ctx, `SELECT flow_id FROM twitch_outputs WHERE clip_id=$1`, clip).Scan(&fid)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := currentFlowPolicy(ctx, tx, fid)
	if err != nil {
		return nil, err
	}
	if rid == 0 {
		if err = tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1`, clip).Scan(&rid); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT i.request FROM twitch_imports i WHERE i.id IN(SELECT source_id FROM twitch_outputs WHERE clip_id=$1 UNION SELECT source_id FROM twitch_compilation_items WHERE compilation_revision_id=$2 UNION SELECT i2.id FROM twitch_compilation_items ci JOIN twitch_imports i2 ON i2.clip_id=ci.source_clip_id WHERE ci.compilation_revision_id=$2)`, clip, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var r twitch.ImportRequest
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		if !sourceMatches(f, r.Source) {
			v := &domain.TwitchPolicyViolation{ClipID: clip, FlowID: fid, Code: "source_filter_changed", Message: "Исходник больше не соответствует каналам или категории потока"}
			if f.Language != "" && (r.Source.Language != f.Language || r.Source.LanguageSource != "clip") {
				v.Code = "language_mismatch"
				v.Message = fmt.Sprintf("Требуется язык %s; Twitch указал %s", f.Language, r.Source.Language)
				if r.Source.LanguageSource != "clip" {
					v.Code = "language_unknown"
					v.Message = "Язык клипа не подтверждён Twitch; язык канала не заменяет язык клипа"
				}
			}
			return v, nil
		}
	}
	return nil, rows.Err()
}

func requireFlowFiltersTx(ctx context.Context, tx pgx.Tx, clip string, rid int64) error {
	v, err := flowFilterViolationTx(ctx, tx, clip, rid)
	if err != nil {
		return err
	}
	if v != nil {
		return fmt.Errorf("%w: %s", domain.ErrConflict, v.Message)
	}
	return nil
}
