package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"time"
)

func (s *Store) SaveTwitchStoragePressure(ctx context.Context, p domain.TwitchStoragePressure) error {
	_, err := s.pool.Exec(ctx, `UPDATE twitch_storage_pressure SET required=$1,state=$2,available_bytes=$3,total_bytes=$4,observed_at=$5,error=$6,reason=$7 WHERE id=1`, p.Required, p.State, p.AvailableBytes, p.TotalBytes, p.ObservedAt, p.Error, p.Reason)
	return err
}
func (s *Store) twitchStoragePressure(ctx context.Context) (domain.TwitchStoragePressure, error) {
	var p domain.TwitchStoragePressure
	err := s.pool.QueryRow(ctx, `SELECT required,state,available_bytes,total_bytes,observed_at,error,reason FROM twitch_storage_pressure WHERE id=1`).Scan(&p.Required, &p.State, &p.AvailableBytes, &p.TotalBytes, &p.ObservedAt, &p.Error, &p.Reason)
	return p, err
}

func storagePreparationAvailable(ctx context.Context, tx pgx.Tx, now time.Time) error {
	var required bool
	var state, reason, detail string
	var observed time.Time
	if err := tx.QueryRow(ctx, `SELECT required,state,observed_at,reason,error FROM twitch_storage_pressure WHERE id=1`).Scan(&required, &state, &observed, &reason, &detail); err != nil {
		return err
	}
	if !required {
		return nil
	}
	if (state != "ok" && state != "low") || now.Sub(observed) > 3*time.Minute || observed.After(now.Add(time.Minute)) {
		if reason == "" {
			reason = "Свежие данные о свободном месте недоступны; подготовка приостановлена"
		}
		if detail != "" {
			reason += ". " + detail
		}
		return waitPreparation("storage_status_unknown", reason, now)
	}
	if state == "low" {
		return waitPreparation("storage_low", reason, now)
	}
	return nil
}
