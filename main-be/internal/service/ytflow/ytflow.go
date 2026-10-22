// Package ytflow finds popular YouTube Shorts and republishes them on our own channels.
//
// Discovery uses the channel RSS feed as a free sentinel and the YouTube Data API (through the
// token of any connected account, held by gpu-be) for statistics. Every API call is paid from a
// per-flow budget inside a shared daily pool; publishing goes through ordinary jobs, so the
// calendar and the daily Shorts limit of the destination channel are shared with Twitch flows.
package ytflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/jobs"
	"terex/proto/msg"
	"terex/proto/workflow"
)

var (
	// ErrQuotaPaused means reads are stopped: either Google reported quotaExceeded today or the
	// shared pool is spent. It is a state, not a failure of the flow.
	ErrQuotaPaused = errors.New("YouTube API quota is exhausted for today")
	// ErrFlowBudget means this flow spent its own daily budget.
	ErrFlowBudget = errors.New("flow quota budget is spent for today")
	// ErrNoReader means no connected YouTube account could serve the read.
	ErrNoReader = errors.New("no working YouTube account to read with")
)

type Gateway interface {
	PublicVideos(context.Context, *workflow.PublicVideosRequest) (*workflow.PublicVideosResponse, error)
}

type Feed interface {
	ResolveChannel(ctx context.Context, url string) (string, error)
	Latest(ctx context.Context, channelID string) ([]domain.FeedVideo, error)
	IsShort(ctx context.Context, videoID string) (bool, error)
}

type JobCreator interface {
	CreateFlowJob(ctx context.Context, card domain.VideoCard, fj jobs.FlowJob) (string, error)
}

type Config struct {
	QuotaDaily     int           // the whole Google project quota per Pacific day
	ReservePublish int           // kept for uploads
	ReserveStats   int           // kept for the stats collector
	RSSEvery       time.Duration // minimum pause between RSS reads of one source
}

// Pool is what flows may spend together.
func (c Config) Pool() int { return max(c.QuotaDaily-c.ReservePublish-c.ReserveStats, 0) }

type Service struct {
	Repo    domain.YTFlowRepo
	Gateway Gateway
	Feed    Feed
	Jobs    JobCreator
	Cfg     Config
	Now     func() time.Time

	mu       sync.Mutex // one pass at a time: cron tick and "run now"
	lastGood int64      // account that served the last read; tried first
}

var pacific = func() *time.Location {
	l, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return l
}()

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// quotaDay is the Pacific date: Google resets the quota at its midnight, not ours.
func (s *Service) quotaDay(t time.Time) time.Time { return t.In(pacific) }

var slotRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Defaults fills unset fields; zero means "use the default" for fields where zero is meaningless.
func Defaults(c domain.YTFlowConfig) domain.YTFlowConfig {
	if c.WindowHours <= 0 {
		c.WindowHours = 72
	}
	if c.MaxVideos <= 0 {
		c.MaxVideos = 200
	}
	if c.MinViews <= 0 {
		c.MinViews = 5000
	}
	if c.MinDurationSec <= 0 {
		c.MinDurationSec = 3
	}
	if c.MaxDurationSec <= 0 {
		c.MaxDurationSec = 180
	}
	if len(c.Slots) == 0 {
		c.Slots = []string{"09:00", "12:00", "15:00", "18:00", "21:00"}
	}
	if c.ApproveMode != domain.ApproveAuto {
		c.ApproveMode = domain.ApproveBy
	}
	if c.QuotaDaily <= 0 {
		c.QuotaDaily = 1000
	}
	if c.RefreshHours <= 0 {
		c.RefreshHours = 6
	}
	if c.ScanHours <= 0 {
		c.ScanHours = 6
	}
	if c.BrandSubtitles == nil {
		on := true
		c.BrandSubtitles = &on
	}
	return c
}

func (s *Service) validate(ctx context.Context, f domain.YTFlow, create bool) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{domain.ErrInvalid}, a...)...)
	}
	if strings.TrimSpace(f.Name) == "" || len([]rune(f.Name)) > 80 {
		return bad("name is required (up to 80 characters)")
	}
	if f.AccountID <= 0 {
		return bad("choose the YouTube account to publish to")
	}
	c := f.Config
	if !c.BrandStyle && (c.BrandBackground != "" || c.BrandBanner != "") {
		return bad("background and banner apply only with the branded style")
	}
	if len([]rune(c.BrandBackground)) > 200 || len([]rune(c.BrandBanner)) > 200 {
		return bad("background/banner name is too long")
	}
	for _, sl := range c.Slots {
		if !slotRe.MatchString(sl) {
			return bad("slot %q must look like 09:00", sl)
		}
	}
	if c.MinDurationSec > c.MaxDurationSec || c.MaxDurationSec > 180 {
		return bad("duration must be within 1..180 seconds")
	}
	if c.MaxVideos > 1000 {
		return bad("scan depth is limited to 1000 videos per channel")
	}
	if c.WindowHours > 24*14 || c.DailyLimit < 0 || c.DailyDownloads < 0 || c.MinViewsPerHour < 0 {
		return bad("invalid limits")
	}
	// the budgets of enabled flows must fit the shared pool, otherwise "hard limit" is a fiction
	if f.Enabled {
		all, err := s.Repo.ListYTFlows(ctx, s.quotaDay(s.now()))
		if err != nil {
			return err
		}
		sum := c.QuotaDaily
		for _, o := range all {
			if o.ID != f.ID && o.Enabled {
				sum += o.Config.QuotaDaily
			}
		}
		if sum > s.Cfg.Pool() {
			return bad("flow quota budgets (%d units) exceed the shared pool (%d units)", sum, s.Cfg.Pool())
		}
	}
	return nil
}

func (s *Service) Flows(ctx context.Context) ([]domain.YTFlow, error) {
	return s.Repo.ListYTFlows(ctx, s.quotaDay(s.now()))
}

func (s *Service) Flow(ctx context.Context, id string) (domain.YTFlow, error) {
	return s.Repo.GetYTFlow(ctx, id, s.quotaDay(s.now()))
}

func (s *Service) Save(ctx context.Context, f domain.YTFlow, create bool) (domain.YTFlow, error) {
	f.Name = strings.TrimSpace(f.Name)
	watermark, err := f.Config.Watermark.Normalize()
	if err != nil {
		return f, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	f.Config.Watermark = watermark
	f.Config = Defaults(f.Config)
	if err := s.validate(ctx, f, create); err != nil {
		return f, err
	}
	return s.Repo.SaveYTFlow(ctx, f, create)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.Repo.DeleteYTFlow(ctx, id) }

// AddSource adds a channel (URL, @handle or UC id) or a search query to a flow.
func (s *Service) AddSource(ctx context.Context, flowID, kind, input string, p domain.YTSourceParams) (domain.YTSource, error) {
	input = strings.TrimSpace(input)
	if input == "" || len([]rune(input)) > 200 {
		return domain.YTSource{}, fmt.Errorf("%w: source is required", domain.ErrInvalid)
	}
	if _, err := s.Repo.GetYTFlow(ctx, flowID, s.quotaDay(s.now())); err != nil {
		return domain.YTSource{}, err
	}
	src := domain.YTSource{FlowID: flowID, Kind: kind, Value: input, Params: p}
	switch kind {
	case domain.YTSourceChannel:
		id, err := s.Feed.ResolveChannel(ctx, input)
		if err != nil {
			return src, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
		}
		src.Value = id
	case domain.YTSourceSearch:
		src.Title = input
	default:
		return src, fmt.Errorf("%w: unknown source kind", domain.ErrInvalid)
	}
	return s.Repo.AddYTSource(ctx, src)
}

func (s *Service) DeleteSource(ctx context.Context, flowID string, id int64) error {
	return s.Repo.DeleteYTSource(ctx, flowID, id)
}

func (s *Service) Candidates(ctx context.Context, flowID, state string, limit int) ([]domain.YTCandidate, error) {
	return s.Repo.ListYTCandidates(ctx, flowID, state, limit)
}

func (s *Service) SkipCandidate(ctx context.Context, flowID, videoID string) error {
	return s.Repo.SkipYTCandidate(ctx, flowID, videoID, "skipped manually")
}

func (s *Service) Quota(ctx context.Context) (domain.YTQuotaReport, error) {
	return s.Repo.YTQuotaReport(ctx, s.quotaDay(s.now()), s.Cfg.Pool())
}

// ClipTitle gives the original title to the clip of a flow job (the whole-video interval has none).
func (s *Service) ClipTitle(ctx context.Context, jobID string) (string, bool, error) {
	t, ok, err := s.Repo.YTClipTitle(ctx, jobID)
	if t = strings.TrimSpace(t); len([]rune(t)) > 100 {
		t = string([]rune(t)[:100])
	}
	return t, ok && t != "", err
}

// RunNow runs one pass for a single flow, ignoring the pause between source checks.
func (s *Service) RunNow(ctx context.Context, flowID string) (domain.YTFlow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.Flow(ctx, flowID)
	if err != nil {
		return f, err
	}
	if !f.Enabled {
		return f, fmt.Errorf("%w: the flow is paused", domain.ErrConflict)
	}
	s.runFlow(ctx, f, s.now(), true)
	return s.Flow(ctx, flowID)
}

// Tick runs every enabled flow once; call it often, source checks are paced internally.
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Repo.SyncYTCandidates(ctx); err != nil {
		return err
	}
	flows, err := s.Repo.ListYTFlows(ctx, s.quotaDay(s.now()))
	if err != nil {
		return err
	}
	now := s.now()
	for _, f := range flows {
		if f.Enabled {
			s.runFlow(ctx, f, now, false)
		}
	}
	return nil
}

func (s *Service) runFlow(ctx context.Context, f domain.YTFlow, now time.Time, force bool) {
	f.Config = Defaults(f.Config)
	status, msg := "ok", ""
	if err := s.discover(ctx, f, now, force); err != nil {
		status, msg = classify(err)
	}
	// Scheduling needs no API quota: waiting candidates are published even when reads are paused.
	wait, err := s.schedule(ctx, f, now)
	if err == nil && status == "ok" && wait != "" {
		status, msg = "waiting", waitingNote(wait)
	}
	if err != nil {
		slog.ErrorContext(ctx, "youtube flow scheduling failed", "flow", f.ID, "err", err)
		if status == "ok" {
			status, msg = "error", err.Error()
		}
	}
	if err := s.Repo.MarkYTFlowRun(ctx, f.ID, now, status, msg); err != nil {
		slog.ErrorContext(ctx, "youtube flow status save failed", "flow", f.ID, "err", err)
	}
}

func classify(err error) (string, string) {
	switch {
	case errors.Is(err, ErrQuotaPaused):
		return "quota", err.Error()
	case errors.Is(err, ErrFlowBudget):
		return "budget", err.Error()
	case errors.Is(err, ErrNoReader):
		return "no_account", err.Error()
	}
	return "error", err.Error()
}

func paused(err error) bool {
	return errors.Is(err, ErrQuotaPaused) || errors.Is(err, ErrFlowBudget) || errors.Is(err, ErrNoReader)
}

// schedule binds waiting candidates to free calendar slots and creates their jobs.
func (s *Service) schedule(ctx context.Context, f domain.YTFlow, now time.Time) (string, error) {
	res, wait, err := s.Repo.ReserveYTSlots(ctx, f.ID, now)
	if err != nil {
		return "", err
	}
	var firstErr error
	for _, r := range res {
		c := r.Candidate
		slot := r.SlotAt
		card := domain.VideoCard{
			YoutubeURL:  "https://www.youtube.com/watch?v=" + c.VideoID,
			SliceMode:   string(msg.SliceWhole),
			ApproveMode: f.Config.ApproveMode,
			Targets:     []domain.AccountTarget{{AccountID: f.AccountID, StartAt: &slot, Timezone: r.Timezone}},
			Watermark:   f.Config.Watermark,
		}
		applyBrand(&card, f.Config)
		jobID, err := s.Jobs.CreateFlowJob(ctx, card, jobs.FlowJob{SourceKind: domain.YTJobSourceKind, SourceID: c.VideoID, OutputFormat: domain.YTOutputFormat})
		if jobID != "" { // a job that failed to dispatch still exists: sync turns it into a failed candidate
			if e := s.Repo.AttachYTJob(ctx, f.ID, c.VideoID, jobID); e != nil && firstErr == nil {
				firstErr = e
			}
		}
		if err != nil {
			slog.ErrorContext(ctx, "youtube flow job failed", "flow", f.ID, "video", c.VideoID, "job", jobID, "err", err)
			if jobID == "" {
				if e := s.Repo.ReleaseYTReservation(ctx, f.ID, c.VideoID, "job creation failed: "+err.Error(), false); e != nil && firstErr == nil {
					firstErr = e
				}
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		slog.InfoContext(ctx, "youtube flow job created", "flow", f.ID, "video", c.VideoID, "job", jobID, "slot", slot, "views", c.Views, "score", c.Score)
	}
	return wait, firstErr
}

// Branded layout: the video takes this share of the canvas width, the audio is shifted by this many semitones.
const (
	brandVideoScale     = 0.75
	brandPitchSemitones = 2.0
)

// applyBrand turns the flow's "фирменный стиль" settings into job options: the layout itself,
// the chosen background (blur when empty) and the banner variant at the bottom.
func applyBrand(card *domain.VideoCard, c domain.YTFlowConfig) {
	if !c.BrandStyle {
		return
	}
	card.Brand = &msg.BrandOptions{VideoScale: brandVideoScale, PitchSemitones: brandPitchSemitones, NoSubtitles: c.BrandSubtitles != nil && !*c.BrandSubtitles}
	if c.BrandBackground != "" {
		card.AddBackground, card.BackgroundName = true, c.BrandBackground
	}
	if c.BrandBanner != "" {
		card.Banners = []domain.BannerConfig{{BannerName: c.BrandBanner, Targets: card.Targets}}
	}
}

// waitingNote explains in the panel why a flow with candidates published nothing.
func waitingNote(reason string) string {
	switch reason {
	case domain.YTWaitNoSlots:
		return "Нет свободных окон: дневной лимит канала и расписание (общие с Twitch-потоками) заняты. Видео ждут первого свободного окна."
	case domain.YTWaitFlowLimit:
		return "Достигнут собственный лимит потока (в день или скачиваний в день)."
	case domain.YTWaitNoCandidates:
		return "Нет видео, подходящих под фильтры."
	}
	return ""
}
