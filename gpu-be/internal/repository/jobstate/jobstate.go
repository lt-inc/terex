package jobstate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"terex/gpu-be/internal/domain"
)

var (
	statusBucket = []byte("status")
	clipsBucket  = []byte("clips")
	ErrNotFound  = errors.New("job not found")
)

type Store struct{ db *bolt.DB }

func Open(dir string) (*Store, error) {
	db, err := bolt.Open(filepath.Join(dir, "jobs.db"), 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{statusBucket, clipsBucket} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	return &Store{db: db}, err
}

func (s *Store) Close() error { return s.db.Close() }

type statusRec struct {
	domain.JobStatus
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) SetStatus(_ context.Context, st domain.JobStatus) error {
	raw, _ := json.Marshal(statusRec{JobStatus: st, UpdatedAt: time.Now().UTC()})
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(statusBucket).Put([]byte(st.JobID), raw) })
}

func (s *Store) GetStatus(_ context.Context, jobID string) (domain.JobStatus, time.Time, error) {
	var rec statusRec
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(statusBucket).Get([]byte(jobID))
		if v == nil {
			return ErrNotFound
		}
		return json.Unmarshal(v, &rec)
	})
	return rec.JobStatus, rec.UpdatedAt, err
}

func (s *Store) SaveClips(_ context.Context, jobID string, clips []domain.Clip) error {
	raw, _ := json.Marshal(clips)
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(clipsBucket).Put([]byte(jobID), raw) })
}

func (s *Store) ListClips(_ context.Context, jobID string) ([]domain.Clip, error) {
	var clips []domain.Clip
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(clipsBucket).Get([]byte(jobID))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &clips)
	})
	return clips, err
}
