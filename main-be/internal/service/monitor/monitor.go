// Package monitor watches YouTube channels and creates jobs for new videos; it also manages channels.
package monitor

import (
	"context"
	"log/slog"
	"time"

	"terex/main-be/internal/domain"
)

// JobCreator is satisfied by jobs.Service.
type JobCreator interface {
	Create(ctx context.Context, card domain.VideoCard, channelID *int64) (string, error)
}

type Service struct {
	Channels domain.ChannelRepo
	Feed     domain.Feed
	Jobs     JobCreator
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) List(ctx context.Context) ([]domain.Channel, error) {
	return s.Channels.ListChannels(ctx)
}

// Add resolves the channel id from its URL and stores the channel; existing videos are baselined
// so that only videos published after this moment trigger jobs.
func (s *Service) Add(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	id, err := s.Feed.ResolveChannel(ctx, c.URL)
	if err != nil {
		return c, err
	}
	c.YTChannelID = id
	if c.ApproveMode != domain.ApproveAuto {
		c.ApproveMode = domain.ApproveBy
	}
	created, err := s.Channels.CreateChannel(ctx, c)
	if err != nil {
		return created, err
	}
	if err := s.baseline(ctx, created); err != nil {
		slog.WarnContext(ctx, "channel baseline failed; first tick will baseline", "channel", created.ID, "err", err)
	}
	return created, nil
}

func (s *Service) baseline(ctx context.Context, c domain.Channel) error {
	vids, err := s.Feed.Latest(ctx, c.YTChannelID)
	if err != nil {
		return err
	}
	ids := make([]string, len(vids))
	for i, v := range vids {
		ids[i] = v.ID
	}
	if _, err := s.Channels.MarkSeen(ctx, c.ID, ids); err != nil {
		return err
	}
	return s.Channels.TouchChannel(ctx, c.ID, s.now())
}

func (s *Service) Update(ctx context.Context, id int64, u domain.ChannelUpdate) (domain.Channel, error) {
	return s.Channels.UpdateChannel(ctx, id, u)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.Channels.DeleteChannel(ctx, id)
}

// Tick checks every enabled channel once; call periodically.
func (s *Service) Tick(ctx context.Context) error {
	chans, err := s.Channels.ListChannels(ctx)
	if err != nil {
		return err
	}
	for _, c := range chans {
		if !c.Enabled {
			continue
		}
		if c.LastCheckedAt == nil {
			if err := s.baseline(ctx, c); err != nil {
				slog.ErrorContext(ctx, "baseline failed", "channel", c.ID, "err", err)
			}
			continue
		}
		vids, err := s.Feed.Latest(ctx, c.YTChannelID)
		if err != nil {
			slog.ErrorContext(ctx, "feed failed", "channel", c.ID, "err", err)
			continue
		}
		ids := make([]string, len(vids))
		byID := map[string]domain.FeedVideo{}
		for i, v := range vids {
			ids[i] = v.ID
			byID[v.ID] = v
		}
		fresh, err := s.Channels.MarkSeen(ctx, c.ID, ids)
		if err != nil {
			slog.ErrorContext(ctx, "mark seen failed", "channel", c.ID, "err", err)
			continue
		}
		for i := len(fresh) - 1; i >= 0; i-- { // feed is newest-first; create oldest first
			card := c.Spec
			card.YoutubeURL = byID[fresh[i]].URL
			card.ApproveMode = c.ApproveMode
			cid := c.ID
			if jobID, err := s.Jobs.Create(ctx, card, &cid); err != nil {
				slog.ErrorContext(ctx, "create job failed", "channel", c.ID, "video", fresh[i], "err", err)
			} else {
				slog.InfoContext(ctx, "job created from channel", "channel", c.ID, "video", fresh[i], "job", jobID)
			}
		}
		_ = s.Channels.TouchChannel(ctx, c.ID, s.now())
	}
	return nil
}
