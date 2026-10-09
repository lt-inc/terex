package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

type windowedHelix interface {
	BeginDiscoveryCycle(context.Context) context.Context
	DiscoveryPhase(context.Context, int) context.Context
	DiscoveryRemaining(context.Context) int
	DiscoveryUsed(context.Context) int
	GetClipPage(context.Context, twitch.Flow, domain.Channel, twitch.DiscoveryWindow) (domain.ClipPage, error)
	GetClipsByIDs(context.Context, []string) ([]domain.Clip, error)
	ReportDiscovery(domain.DiscoveryReport)
}
type discoveryProgressStore interface {
	LoadDiscoveryProgress(context.Context, string, string, int64, int) (twitch.DiscoveryProgress, error)
	SaveDiscoveryProgress(context.Context, string, string, twitch.DiscoveryProgress) error
}
type discoveryResult struct {
	clips    []domain.Clip
	err      error
	progress twitch.DiscoveryProgress
	target   string
	flow     string
}
type discoveryRun struct {
	flow    twitch.Flow
	channel domain.Channel
	result  *discoveryResult
	hot     twitch.DiscoveryProgress
	report  domain.DiscoveryReport
}

func discoveryKey(flow, target string) string { return flow + ":" + target }
func discoveryTarget(f twitch.Flow, ch domain.Channel) string {
	if f.SourceMode == "category" {
		return "category:" + f.CategoryID
	}
	return ch.BroadcasterID
}

func equalDiscoveryWindows(start, end time.Time) []twitch.DiscoveryWindow {
	if !end.After(start) {
		return nil
	}
	span := end.Sub(start)
	n := int((span + 8*time.Hour - 1) / (8 * time.Hour))
	out := make([]twitch.DiscoveryWindow, 0, n)
	// Newest first avoids starving recent metadata when a pass is incomplete.
	for i := n - 1; i >= 0; i-- {
		a := start.Add(time.Duration(int64(span) * int64(i) / int64(n)))
		b := start.Add(time.Duration(int64(span) * int64(i+1) / int64(n)))
		out = append(out, twitch.DiscoveryWindow{Start: a, End: b})
	}
	return out
}
func prepareDiscoveryProgress(p twitch.DiscoveryProgress, epoch int64, version int, start, end time.Time) twitch.DiscoveryProgress {
	if p.Epoch != epoch || p.FlowVersion != version || p.AsOf.IsZero() || len(p.Pending) == 0 {
		windows := equalDiscoveryWindows(start, end)
		return twitch.DiscoveryProgress{Epoch: epoch, FlowVersion: version, AsOf: end, Pending: windows, Total: len(windows)}
	}
	pending := p.Pending[:0]
	for _, w := range p.Pending {
		w.Restarts = 0
		if !w.End.After(start) {
			p.Completed++
			continue
		}
		if w.Start.Before(start) {
			w.Start = start
			w.Cursor = ""
			w.Pages = 0
			w.SeenCursors = nil
		}
		pending = append(pending, w)
	}
	p.Pending = pending
	return p
}

// Advance only after the page has been received; caller persists progress after
// Terex accepts its observations, so a crash replays harmless metadata.
func advanceDiscovery(p *twitch.DiscoveryProgress, page domain.ClipPage, minViews int) {
	w := p.Pending[0]
	p.Pending = p.Pending[1:]
	w.Pages++
	if page.Count == 0 || page.LastViews < minViews {
		p.Completed++
		return
	}
	if w.Pages >= 10 && page.Count >= 100 {
		if w.End.Sub(w.Start) >= 30*time.Minute {
			mid := w.Start.Add(w.End.Sub(w.Start) / 2)
			p.Total++
			p.Pending = append(p.Pending, twitch.DiscoveryWindow{Start: mid, End: w.End}, twitch.DiscoveryWindow{Start: w.Start, End: mid})
			return
		}
		p.Completed++
		p.Truncated = true
		return
	}
	if page.Cursor == "" {
		p.Completed++
		return
	}
	if w.Cursor != "" {
		w.SeenCursors = append(w.SeenCursors, w.Cursor)
	}
	w.Cursor = page.Cursor
	p.Pending = append(p.Pending, w)
}

func (s *Service) discoverWindowed(ctx context.Context, cfg twitch.CollectorConfig, flows []twitch.Flow, catalogue map[string]domain.Channel, only string, force bool, now time.Time) (map[string]*discoveryResult, bool) {
	h, ok := s.twitch.(windowedHelix)
	if !ok {
		return nil, false
	}
	store, ok := s.store.(discoveryProgressStore)
	if !ok {
		return nil, false
	}
	ctx = h.BeginDiscoveryCycle(ctx)
	budget := h.DiscoveryRemaining(ctx)
	results := map[string]*discoveryResult{}
	runs := []*discoveryRun{}
	flowAPI := s.twitch.(domain.FlowTwitch)
	for _, f := range flows {
		if !f.Enabled || (only != "" && only != f.ID) {
			continue
		}
		interval := f.PollIntervalMinutes
		if interval < 1 {
			interval = cfg.Settings.PollIntervalMinutes
		}
		s.mu.Lock()
		last := s.flowStarted[f.ID]
		s.mu.Unlock()
		if !force && now.Sub(last) < time.Duration(interval)*time.Minute {
			continue
		}
		targets := []domain.Channel{{}}
		if f.SourceMode != "category" {
			targets = nil
			if f.ID == "default" && len(f.BroadcasterIDs) == 0 {
				if v, e := s.store.(domain.FlowStore).LegacyBroadcasterIDs(ctx); e == nil {
					f.BroadcasterIDs = v
				}
			}
			for _, id := range f.BroadcasterIDs {
				ch, exists := catalogue[id]
				if !exists {
					var e error
					ch, e = flowAPI.ResolveBroadcaster(ctx, id)
					if e != nil {
						continue
					}
					ch, e = s.store.CreateChannel(ctx, ch)
					if e != nil {
						continue
					}
					catalogue[id] = ch
				}
				if !ch.Paused && !ch.Archived {
					targets = append(targets, ch)
				}
			}
		}
		for _, ch := range targets {
			target := discoveryTarget(f, ch)
			p, e := store.LoadDiscoveryProgress(ctx, f.ID, target, cfg.Epoch, f.Version)
			r := &discoveryResult{target: target, flow: f.ID, err: e}
			results[discoveryKey(f.ID, target)] = r
			if e != nil {
				continue
			}
			start := now.Add(-time.Duration(f.WindowHours) * time.Hour)
			r.progress = prepareDiscoveryProgress(p, cfg.Epoch, f.Version, start, now)
			hotStart := now.Add(-8 * time.Hour)
			if hotStart.Before(start) {
				hotStart = start
			}
			runs = append(runs, &discoveryRun{flow: f, channel: ch, result: r, hot: twitch.DiscoveryProgress{AsOf: now, Pending: []twitch.DiscoveryWindow{{Start: hotStart, End: now}}, Total: 1}, report: domain.DiscoveryReport{FlowID: f.ID, BroadcasterID: ch.BroadcasterID, Language: f.Language, CheckedAt: now}})
		}
	}
	if len(runs) == 0 {
		return results, true
	}
	step := func(phase context.Context, r *discoveryRun, hot bool) bool {
		p := &r.result.progress
		if hot {
			p = &r.hot
		}
		if len(p.Pending) == 0 {
			return false
		}
		w := p.Pending[0]
		before := h.DiscoveryUsed(phase)
		page, e := h.GetClipPage(phase, r.flow, r.channel, w)
		r.report.Coverage = ensureCoverage(r.report.Coverage)
		r.report.Coverage.Requests += h.DiscoveryUsed(phase) - before
		if e != nil {
			var f *domain.Failure
			if errors.As(e, &f) && f.HTTPStatus == 400 && w.Cursor != "" && w.Restarts < 1 {
				p.Pending[0].Cursor = ""
				p.Pending[0].Pages = 0
				p.Pending[0].SeenCursors = nil
				p.Pending[0].Restarts++
				return true
			}
			r.report.Error = safeErrorMessage(e, "Не удалось продолжить поиск Twitch")
			r.report.Coverage.Reason = "request_failed"
			if errors.As(e, &f) && (f.Code == "discovery_budget" || f.Code == "twitch_rate_limit") {
				r.report.Coverage.Reason = f.Code
				return false
			}
			// Retain the work for the next due check, but do not retry indefinitely.
			r.result.err = e
			return false
		}
		r.report.Pages++
		r.result.clips = append(r.result.clips, page.Clips...)
		if repeatedDiscoveryCursor(w, page.Cursor) {
			p.Pending[0].Cursor = ""
			p.Pending[0].Pages = 0
			p.Pending[0].SeenCursors = nil
			if w.Restarts < 1 {
				p.Pending[0].Restarts++
				return true
			}
			r.report.Coverage.Reason = "repeated_cursor"
			return false
		}
		advanceDiscovery(p, page, r.flow.MinViews)
		return true
	}
	runPhase := func(phase context.Context, hot, once bool) {
		blocked := map[*discoveryRun]bool{}
		for {
			active := false
			for _, r := range runs {
				p := &r.result.progress
				if hot {
					p = &r.hot
				}
				if blocked[r] || len(p.Pending) == 0 {
					continue
				}
				if h.DiscoveryRemaining(phase) <= 0 {
					return
				}
				if step(phase, r, hot) {
					active = true
				} else {
					blocked[r] = true
				}
			}
			if once || !active {
				return
			}
		}
	}
	hotCtx := h.DiscoveryPhase(ctx, budget/2)
	// Give every target a recent page before spending the remaining hot budget.
	runPhase(hotCtx, true, true)
	if watch, ok := s.terex.(interface {
		ObservationCandidates(context.Context, string) ([]twitch.Source, error)
	}); ok {
		wanted := map[string][]*discoveryRun{}
		ids := []string{}
		seenFlows := map[string]bool{}
		for _, r := range runs {
			if seenFlows[r.flow.ID] {
				continue
			}
			seenFlows[r.flow.ID] = true
			sources, e := watch.ObservationCandidates(hotCtx, r.flow.ID)
			if e != nil {
				s.log.WarnContext(ctx, "collector growth watchlist unavailable", "flow_id", r.flow.ID, "reason", safeErrorMessage(e, "watchlist unavailable"))
				continue
			}
			for n, src := range sources {
				if n >= 200 {
					break
				}
				if _, exists := wanted[src.ClipID]; !exists {
					ids = append(ids, src.ClipID)
				}
				for _, target := range runs {
					if target.flow.ID == r.flow.ID && (r.flow.SourceMode == "category" || target.channel.BroadcasterID == src.BroadcasterID) {
						wanted[src.ClipID] = append(wanted[src.ClipID], target)
					}
				}
			}
		}
		for offset := 0; offset < len(ids) && h.DiscoveryRemaining(hotCtx) > 0; offset += 100 {
			end := min(offset+100, len(ids))
			before := h.DiscoveryUsed(hotCtx)
			clips, e := h.GetClipsByIDs(hotCtx, ids[offset:end])
			if e != nil {
				break
			}
			for _, clip := range clips {
				for _, r := range wanted[clip.ID] {
					r.result.clips = append(r.result.clips, clip)
					r.report.Coverage = ensureCoverage(r.report.Coverage)
					r.report.Coverage.RefreshClips++
				}
			}
			if len(runs) > 0 {
				runs[0].report.Coverage = ensureCoverage(runs[0].report.Coverage)
				runs[0].report.Coverage.Requests += h.DiscoveryUsed(hotCtx) - before
			}
		}
	}
	runPhase(hotCtx, true, false)
	backCtx := h.DiscoveryPhase(ctx, h.DiscoveryRemaining(ctx))
	runPhase(backCtx, false, false)
	if h.DiscoveryRemaining(ctx) > 0 {
		runPhase(h.DiscoveryPhase(ctx, h.DiscoveryRemaining(ctx)), true, false)
	}
	for _, r := range runs {
		// A clip may be returned by overlapping hot/history windows and ID refresh.
		latest := map[string]domain.Clip{}
		order := []string{}
		for _, v := range r.result.clips {
			old, exists := latest[v.ID]
			if !exists {
				order = append(order, v.ID)
			}
			if !exists || !v.ObservedAt.Before(old.ObservedAt) {
				latest[v.ID] = v
			}
		}
		r.result.clips = r.result.clips[:0]
		for _, id := range order {
			r.result.clips = append(r.result.clips, latest[id])
		}
		coverage := ensureCoverage(r.report.Coverage)
		r.report.Coverage = coverage
		coverage.AsOf = r.result.progress.AsOf
		coverage.WindowsTotal = r.result.progress.Total
		coverage.WindowsCompleted = r.result.progress.Completed
		coverage.Complete = len(r.result.progress.Pending) == 0 && !r.result.progress.Truncated && len(r.hot.Pending) == 0 && !r.hot.Truncated
		if !coverage.Complete && coverage.Reason == "" {
			coverage.Reason = "discovery_budget"
		}
		if r.result.progress.Truncated || r.hot.Truncated {
			coverage.Reason = "dense_minimum_window"
		}
		r.report.PageLimitReached = !coverage.Complete
		r.report.Fetched = len(r.result.clips)
		for _, v := range r.result.clips {
			if r.flow.Language == "" || v.Language == r.flow.Language && v.LanguageSource == "clip" {
				r.report.LanguageMatched++
			} else if v.LanguageSource != "clip" {
				r.report.LanguageUnknown++
			} else {
				r.report.LanguageRejected++
			}
		}
		h.ReportDiscovery(r.report)
		s.log.InfoContext(ctx, "collector windowed discovery finished", "flow_id", r.flow.ID, "target", r.result.target, "fetched", r.report.Fetched, "windows_completed", coverage.WindowsCompleted, "windows_total", coverage.WindowsTotal, "requests", coverage.Requests, "complete", coverage.Complete, "reason", coverage.Reason)
	}
	return results, true
}
func repeatedDiscoveryCursor(w twitch.DiscoveryWindow, next string) bool {
	if next == "" {
		return false
	}
	if next == w.Cursor {
		return true
	}
	for _, seen := range w.SeenCursors {
		if next == seen {
			return true
		}
	}
	return false
}
func ensureCoverage(v *twitch.DiscoveryCoverage) *twitch.DiscoveryCoverage {
	if v == nil {
		return &twitch.DiscoveryCoverage{}
	}
	return v
}

func (s *Service) saveDiscoveryProgress(ctx context.Context, flow string, results map[string]*discoveryResult) error {
	store, ok := s.store.(discoveryProgressStore)
	if !ok {
		return nil
	}
	for _, r := range results {
		if r.flow == flow {
			if e := store.SaveDiscoveryProgress(ctx, flow, r.target, r.progress); e != nil {
				return fmt.Errorf("save discovery progress: %w", e)
			}
		}
	}
	return nil
}
