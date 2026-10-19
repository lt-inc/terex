package ytflow

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/workflow"
)

const batch = 50 // YouTube list endpoints return at most 50 items per call

// estimate is the quota price reserved before a read; the real price settles it afterwards.
func estimate(req workflow.PublicVideosRequest) int {
	pages := func(n int) int { return (max(n, 1) + batch - 1) / batch }
	switch req.Op {
	case workflow.PublicChannelShorts:
		return 2 * pages(req.MaxResults) // playlist pages + statistics
	case workflow.PublicSearch:
		return 100 + 1
	default:
		return pages(len(req.VideoIDs))
	}
}

// read performs one public-data read for a flow. The estimated quota is reserved first (so the
// flow budget and the shared pool are hard limits), then settled with the real cost. Accounts are
// tried in order, starting with the one that worked last; an account that needs re-auth or fails
// is skipped in favour of the next one.
func (s *Service) read(ctx context.Context, f domain.YTFlow, req workflow.PublicVideosRequest) ([]workflow.PublicVideo, error) {
	day := s.quotaDay(s.now())
	if tripped, err := s.Repo.YTBreakerTripped(ctx, day); err != nil {
		return nil, err
	} else if tripped {
		return nil, ErrQuotaPaused
	}
	est := estimate(req)
	ok, err := s.Repo.ReserveYTQuota(ctx, day, f.ID, est, f.Config.QuotaDaily, s.Cfg.Pool())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, s.explainRefusal(ctx, f, day)
	}
	readers, err := s.Repo.YTReaders(ctx)
	if err != nil {
		_ = s.Repo.AdjustYTQuota(ctx, day, f.ID, -est)
		return nil, err
	}
	if len(readers) == 0 {
		_ = s.Repo.AdjustYTQuota(ctx, day, f.ID, -est)
		return nil, ErrNoReader
	}
	// the account that worked last goes first
	for i, r := range readers {
		if r.AccountID == s.lastGood && i > 0 {
			readers[0], readers[i] = readers[i], readers[0]
			break
		}
	}
	spent := 0
	settle := func() { _ = s.Repo.AdjustYTQuota(ctx, day, f.ID, spent-est) }
	var lastErr string
	for _, r := range readers {
		q := req
		q.AccountID, q.CredentialRef = r.AccountID, workflow.ObjectRef{Bucket: r.CredentialRef.Bucket, Object: r.CredentialRef.Object}
		q.ExpectedRemoteID = r.ChannelID
		resp, err := s.Gateway.PublicVideos(ctx, &q)
		if err != nil {
			// gpu-be unreachable: no other account will fare better
			_ = s.Repo.AdjustYTQuota(ctx, day, f.ID, -est)
			return nil, err
		}
		spent += resp.QuotaUnits
		switch resp.Status {
		case workflow.StatsOK:
			s.lastGood = r.AccountID
			settle()
			return resp.Videos, nil
		case workflow.StatsQuota:
			settle()
			if e := s.Repo.TripYTBreaker(ctx, day, resp.Error); e != nil {
				slog.ErrorContext(ctx, "quota breaker save failed", "err", e)
			}
			slog.WarnContext(ctx, "YouTube API quota exhausted: flow reads paused until the Pacific day ends", "flow", f.ID, "account", r.Name)
			return nil, ErrQuotaPaused
		default: // needs_reauth, error: try the next account
			lastErr = fmt.Sprintf("account %s: %s", r.Name, resp.Error)
			slog.WarnContext(ctx, "account cannot read YouTube, trying the next one", "flow", f.ID, "account", r.Name, "status", resp.Status, "err", resp.Error)
		}
	}
	settle()
	return nil, fmt.Errorf("%w (%s)", ErrNoReader, lastErr)
}

// explainRefusal tells a spent flow budget from a spent shared pool.
func (s *Service) explainRefusal(ctx context.Context, f domain.YTFlow, day time.Time) error {
	rep, err := s.Repo.YTQuotaReport(ctx, day, s.Cfg.Pool())
	if err != nil {
		return err
	}
	if f.Config.QuotaDaily > 0 && rep.ByFlow[f.ID] >= f.Config.QuotaDaily-1 {
		return ErrFlowBudget
	}
	if rep.Used >= rep.Pool-1 {
		return ErrQuotaPaused
	}
	return ErrFlowBudget // the next read would not fit the remaining budget
}

// discover reads new videos of the flow's sources and refreshes the statistics of waiting ones.
func (s *Service) discover(ctx context.Context, f domain.YTFlow, now time.Time, force bool) error {
	own, err := s.ownChannels(ctx, f)
	if err != nil {
		return err
	}
	var firstErr error
	for _, src := range f.Sources {
		var e error
		switch src.Kind {
		case domain.YTSourceChannel:
			e = s.scanChannel(ctx, f, src, own, now, force)
		case domain.YTSourceSearch:
			e = s.scanSearch(ctx, f, src, own, now, force)
		}
		if e != nil {
			if paused(e) {
				return e // nothing else can be read now
			}
			slog.WarnContext(ctx, "youtube flow source failed", "flow", f.ID, "source", src.Value, "err", e)
			_ = s.Repo.TouchYTSource(ctx, src.ID, nil, nil, e.Error())
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if e := s.refresh(ctx, f, now); e != nil {
		if paused(e) {
			return e
		}
		if firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}

// ownChannels are channels we publish to: their videos are never republished (no loops).
func (s *Service) ownChannels(ctx context.Context, f domain.YTFlow) (map[string]bool, error) {
	readers, err := s.Repo.YTReaders(ctx)
	if err != nil {
		return nil, err
	}
	own := map[string]bool{f.ChannelID: true}
	for _, r := range readers {
		own[r.ChannelID] = true
	}
	return own, nil
}

func due(last *time.Time, every time.Duration, now time.Time) bool {
	return last == nil || now.Sub(*last) >= every
}

func (s *Service) scanChannel(ctx context.Context, f domain.YTFlow, src domain.YTSource, own map[string]bool, now time.Time, force bool) error {
	scanDue := due(src.LastScanAt, time.Duration(f.Config.ScanHours)*time.Hour, now)
	depth := batch
	if f.Config.AllTime {
		// the whole history is read once (or on "check now"); afterwards the RSS sentinel finds new videos
		scanDue = src.LastScanAt == nil || force
		depth = f.Config.MaxVideos
	}
	if !force && !scanDue && !due(src.LastRSSAt, s.Cfg.RSSEvery, now) {
		return nil
	}
	feed, err := s.Feed.Latest(ctx, src.Value)
	if err != nil {
		return fmt.Errorf("RSS: %w", err)
	}
	if err = s.Repo.TouchYTSource(ctx, src.ID, &now, nil, ""); err != nil {
		return err
	}
	ids := make([]string, len(feed))
	for i, v := range feed {
		ids[i] = v.ID
	}
	known, err := s.Repo.YTKnownVideos(ctx, f.ID, ids)
	if err != nil {
		return err
	}
	var unknown []string
	for _, id := range ids {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}

	var videos []workflow.PublicVideo
	switch {
	case scanDue:
		// the Shorts playlist: only Shorts, newest first
		videos, err = s.read(ctx, f, workflow.PublicVideosRequest{Op: workflow.PublicChannelShorts, ChannelID: src.Value, MaxResults: depth})
		if err != nil {
			return err
		}
		scanned := now
		if err = s.Repo.TouchYTSource(ctx, src.ID, nil, &scanned, ""); err != nil {
			return err
		}
	case len(unknown) > 0:
		// something new in the feed: read just those videos (1 unit) and keep the Shorts among them
		videos, err = s.read(ctx, f, workflow.PublicVideosRequest{Op: workflow.PublicVideoStats, VideoIDs: unknown})
		if err != nil {
			return err
		}
		videos = s.onlyShorts(ctx, videos)
	default:
		return nil
	}
	if src.Title == "" && len(videos) > 0 {
		if _, e := s.Repo.AddYTSource(ctx, domain.YTSource{FlowID: f.ID, Kind: src.Kind, Value: src.Value, Title: videos[0].ChannelTitle, Params: src.Params}); e != nil {
			slog.WarnContext(ctx, "source title save failed", "flow", f.ID, "err", e)
		}
	}
	return s.store(ctx, f, &src.ID, videos, known, own, now)
}

func (s *Service) scanSearch(ctx context.Context, f domain.YTFlow, src domain.YTSource, own map[string]bool, now time.Time, force bool) error {
	if !force && !due(src.LastScanAt, time.Duration(f.Config.ScanHours)*time.Hour, now) {
		return nil
	}
	req := workflow.PublicVideosRequest{Op: workflow.PublicSearch, Query: src.Value, RegionCode: src.Params.RegionCode, Language: src.Params.Language, MaxResults: batch}
	if !f.Config.AllTime {
		req.PublishedAfter = now.Add(-time.Duration(f.Config.WindowHours) * time.Hour)
	}
	videos, err := s.read(ctx, f, req)
	if err != nil {
		return err
	}
	scanned := now
	if err = s.Repo.TouchYTSource(ctx, src.ID, nil, &scanned, ""); err != nil {
		return err
	}
	ids := make([]string, len(videos))
	for i, v := range videos {
		ids[i] = v.ID
	}
	known, err := s.Repo.YTKnownVideos(ctx, f.ID, ids)
	if err != nil {
		return err
	}
	// search also returns short horizontal videos: only unknown ones need the (free) Shorts check
	fresh := make([]workflow.PublicVideo, 0, len(videos))
	for _, v := range videos {
		if !known[v.ID] {
			fresh = append(fresh, v)
		}
	}
	fresh = s.onlyShorts(ctx, fresh)
	for _, v := range videos {
		if known[v.ID] {
			fresh = append(fresh, v) // statistics refresh
		}
	}
	return s.store(ctx, f, &src.ID, fresh, known, own, now)
}

// onlyShorts keeps videos that YouTube serves as Shorts. A failed check drops the video for now:
// it is not stored, so the next pass asks again.
func (s *Service) onlyShorts(ctx context.Context, in []workflow.PublicVideo) []workflow.PublicVideo {
	out := in[:0:0]
	for _, v := range in {
		if v.DurationSec > 180 {
			continue
		}
		ok, err := s.Feed.IsShort(ctx, v.ID)
		if err != nil {
			slog.DebugContext(ctx, "shorts check failed", "video", v.ID, "err", err)
			continue
		}
		if ok {
			out = append(out, v)
		}
	}
	return out
}

// store converts videos to candidates. Videos that cannot qualify are stored as skipped with the
// reason, so they are not read again.
func (s *Service) store(ctx context.Context, f domain.YTFlow, sourceID *int64, videos []workflow.PublicVideo, known, own map[string]bool, now time.Time) error {
	cutoff := now.Add(-time.Duration(f.Config.WindowHours) * time.Hour)
	if f.Config.AllTime {
		cutoff = time.Time{}
	}
	list := make([]domain.YTCandidate, 0, len(videos))
	for _, v := range videos {
		published := v.PublishedAt
		c := domain.YTCandidate{VideoID: v.ID, ChannelID: v.ChannelID, ChannelTitle: v.ChannelTitle, Title: v.Title, PublishedAt: &published,
			DurationSec: v.DurationSec, Views: v.Views, Likes: v.Likes, State: domain.YTNew}
		switch {
		case own[v.ChannelID]:
			c.State, c.Reason = domain.YTSkipped, "own channel"
		case v.PublishedAt.Before(cutoff):
			c.State, c.Reason = domain.YTSkipped, "older than the window"
		case v.DurationSec < f.Config.MinDurationSec || v.DurationSec > f.Config.MaxDurationSec:
			c.State, c.Reason = domain.YTSkipped, "duration out of range"
		}
		list = append(list, c)
	}
	return s.Repo.UpsertYTCandidates(ctx, f.ID, sourceID, list, now)
}

// refresh re-reads statistics of waiting candidates: a Short that keeps growing moves up the queue.
func (s *Service) refresh(ctx context.Context, f domain.YTFlow, now time.Time) error {
	publishedAfter := now.Add(-time.Duration(f.Config.WindowHours) * time.Hour)
	if f.Config.AllTime {
		publishedAfter = time.Time{}
	}
	ids, err := s.Repo.YTCandidatesToRefresh(ctx, f.ID, now.Add(-time.Duration(f.Config.RefreshHours)*time.Hour), publishedAfter, 3*batch)
	if err != nil || len(ids) == 0 {
		return err
	}
	videos, err := s.read(ctx, f, workflow.PublicVideosRequest{Op: workflow.PublicVideoStats, VideoIDs: ids})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, v := range videos {
		seen[v.ID] = true
	}
	for _, id := range ids {
		if !seen[id] { // deleted or made private
			if e := s.Repo.SkipYTCandidate(ctx, f.ID, id, "no longer available"); e != nil && e != domain.ErrConflict {
				return e
			}
		}
	}
	return s.store(ctx, f, nil, videos, nil, map[string]bool{}, now)
}
