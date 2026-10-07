// Package service runs the periodic YouTube statistics collection.
package service

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"terex/proto/workflow"
	"terex/stats-collector/internal/store"
)

// errQuota stops a pass: the quota belongs to the whole Google project, so other accounts would fail too.
var errQuota = errors.New("youtube api quota exceeded")

type Config struct {
	Interval        time.Duration // how often a full pass runs
	MaxVideos       int           // most recent uploads tracked per channel
	FullResolutionD int           // days of full-resolution snapshots kept
}

// Gateway is the gpu-be call that reads statistics with an account's own credentials.
type Gateway interface {
	ChannelStats(context.Context, *workflow.ChannelStatsRequest) (*workflow.ChannelStatsResponse, error)
}

type Service struct {
	cfg    Config
	store  *store.Store
	gw     Gateway
	log    *slog.Logger
	lastOK atomic.Int64 // unix seconds of the last pass without error
}

func New(cfg Config, st *store.Store, gw Gateway, log *slog.Logger) *Service {
	return &Service{cfg: cfg, store: st, gw: gw, log: log}
}

// Healthy reports whether a pass succeeded within three intervals (or the service is still warming up).
func (s *Service) Healthy(started time.Time) bool {
	last := time.Unix(s.lastOK.Load(), 0)
	if s.lastOK.Load() == 0 {
		last = started
	}
	return time.Since(last) < 3*s.cfg.Interval+time.Minute
}

// Run blocks until ctx is done, collecting immediately and then every Interval.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		s.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) pass(ctx context.Context) {
	run := store.Run{Started: time.Now()}
	err := s.collect(ctx, &run)
	run.Finished = time.Now()
	if err != nil {
		run.Err = err.Error()
		s.log.Error("stats pass failed", "err", err, "channels", run.Channels, "videos", run.Videos)
	} else {
		s.lastOK.Store(run.Finished.Unix())
		s.log.Info("stats pass done", "channels", run.Channels, "videos", run.Videos, "quota_units", run.Units, "took", run.Finished.Sub(run.Started).Round(time.Millisecond))
	}
	if e := s.store.SaveRun(ctx, run); e != nil {
		s.log.Error("save run failed", "err", e)
	}
}

func (s *Service) collect(ctx context.Context, run *store.Run) error {
	if _, err := s.store.DropRemoved(ctx); err != nil {
		return err
	}
	accounts, err := s.store.Accounts(ctx)
	if err != nil {
		return err
	}
	// one timestamp per pass keeps snapshots of all channels aligned
	ts := time.Now().UTC().Truncate(time.Second)
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, a := range accounts {
		resp, err := s.gw.ChannelStats(ctx, &workflow.ChannelStatsRequest{AccountID: a.ID, CredentialRef: a.CredentialRef, ExpectedRemoteID: a.ChannelID, MaxVideos: s.cfg.MaxVideos})
		if err != nil {
			// gateway unreachable or the request is invalid: nothing else will work either
			return err
		}
		run.Units += resp.QuotaUnits
		switch resp.Status {
		case workflow.StatsOK:
		case workflow.StatsQuota:
			return errQuota
		case workflow.StatsNeedsReauth:
			// the account is waiting for the user to reconnect it; this is not a collector failure
			s.log.Warn("account needs re-auth, skipped", "account", a.Name, "channel", a.ChannelID, "err", resp.Error)
			continue
		default:
			s.log.Warn("account stats failed", "account", a.Name, "channel", a.ChannelID, "err", resp.Error)
			fail(errors.New("stats for account " + a.Name + ": " + resp.Error))
			continue
		}
		if err = s.store.SaveChannel(ctx, a, resp.Channel, ts); err != nil {
			return err
		}
		if err = s.store.SaveVideos(ctx, resp.Channel.ChannelID, resp.Videos, ts); err != nil {
			return err
		}
		run.Channels++
		run.Videos += len(resp.Videos)
	}
	if _, err = s.store.Thin(ctx, s.cfg.FullResolutionD); err != nil {
		s.log.Warn("snapshot thinning failed", "err", err)
	}
	return firstErr
}
