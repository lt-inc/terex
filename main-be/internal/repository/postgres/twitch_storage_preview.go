package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"terex/main-be/internal/domain"
	"terex/proto/workflow"
)

// Previewing an expired revision never issues a new signed URL for its bytes.
func (s *Store) TwitchRevisionMediaAvailable(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var state, bucket, object string
	var manifest []byte
	err = tx.QueryRow(ctx, `SELECT storage_state,COALESCE(bucket,''),COALESCE(object,''),manifest FROM clip_revisions WHERE id=$1`, id).Scan(&state, &bucket, &object, &manifest)
	if err != nil {
		return mapErr(err)
	}
	if state != "available" {
		return fmt.Errorf("%w: файл удалён по сроку хранения", domain.ErrConflict)
	}
	var source workflow.SourceManifest
	if err = json.Unmarshal(manifest, &source); err != nil {
		return err
	}
	refs := []workflow.ObjectRef{{Bucket: bucket, Object: object}, source.Original}
	if source.Assembly != nil {
		for _, item := range source.Assembly.Items {
			refs = append(refs, item.Original)
		}
	}
	if err = storageRefsAvailable(ctx, tx, refs...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
