package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/repository/telegram"
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, args...))
}

// ---- sources ----

func (s *Service) GetSource(ctx context.Context, id int64) (domain.Source, error) {
	return s.store.Source(ctx, id)
}

func (s *Service) GetChannel(ctx context.Context, id int64) (domain.Channel, error) {
	return s.store.Channel(ctx, id)
}

func (s *Service) ListSources(ctx context.Context) ([]domain.Source, error) {
	return s.store.Sources(ctx)
}

func validateSource(x *domain.Source) error {
	x.Name = strings.TrimSpace(x.Name)
	x.URL = strings.TrimSpace(x.URL)
	x.Query = strings.TrimSpace(x.Query)
	if x.Lang == "" {
		x.Lang = "ru"
	}
	if x.FetchIntervalMin == 0 {
		x.FetchIntervalMin = 30
	}
	if x.Name == "" || len([]rune(x.Name)) > 120 {
		return invalid("name is required (up to 120 characters)")
	}
	if x.Lang != "ru" && x.Lang != "en" {
		return invalid("language must be ru or en")
	}
	if x.FetchIntervalMin < 5 || x.FetchIntervalMin > 1440 {
		return invalid("fetch interval must be 5-1440 minutes")
	}
	switch x.Kind {
	case domain.SourceRSS:
		u, err := url.Parse(x.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(x.URL) > 2000 {
			return invalid("a valid http(s) feed URL is required")
		}
		x.Query = ""
	case domain.SourceGNews:
		if x.Query == "" || len([]rune(x.Query)) > 200 {
			return invalid("a search query is required (up to 200 characters)")
		}
		x.URL = ""
	default:
		return invalid("kind must be rss or gnews")
	}
	return nil
}

func (s *Service) CreateSource(ctx context.Context, x domain.Source) (domain.Source, error) {
	if err := validateSource(&x); err != nil {
		return x, err
	}
	return s.store.CreateSource(ctx, x)
}

func (s *Service) UpdateSource(ctx context.Context, x domain.Source) (domain.Source, error) {
	old, err := s.store.Source(ctx, x.ID)
	if err != nil {
		return x, err
	}
	x.Kind = old.Kind // the kind never changes
	if err = validateSource(&x); err != nil {
		return x, err
	}
	return s.store.UpdateSource(ctx, x)
}

func (s *Service) DeleteSource(ctx context.Context, id int64) error {
	return s.store.DeleteSource(ctx, id)
}

type FeedPreview struct {
	Title string        `json:"title"`
	Items []PreviewItem `json:"items"`
}

type PreviewItem struct {
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Published time.Time `json:"published"`
}

// TestFeed fetches a feed without saving anything, so the panel can validate a URL or query.
func (s *Service) TestFeed(ctx context.Context, x domain.Source) (FeedPreview, error) {
	if err := validateSource(&x); err != nil {
		return FeedPreview{}, err
	}
	f, err := s.feeds.Fetch(ctx, feedURL(x))
	if err != nil {
		return FeedPreview{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err)
	}
	out := FeedPreview{Title: f.Title, Items: []PreviewItem{}}
	for i, it := range f.Items {
		if i >= 8 {
			break
		}
		out.Items = append(out.Items, PreviewItem{Title: it.Title, Link: it.Link, Published: it.Published})
	}
	return out, nil
}

// FetchNow downloads one source immediately and runs the processing stages.
func (s *Service) FetchNow(ctx context.Context, id int64) (int, error) {
	src, err := s.store.Source(ctx, id)
	if err != nil {
		return 0, err
	}
	s.pipeMu.Lock()
	defer s.pipeMu.Unlock()
	added, err := s.fetchSource(ctx, src)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", domain.ErrConflict, err)
	}
	if err = s.processNew(ctx); err != nil {
		return added, err
	}
	if err = s.embedPending(ctx); err != nil {
		return added, err
	}
	return added, nil
}

// ---- channels ----

func (s *Service) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	return s.store.Channels(ctx)
}

func (s *Service) SaveChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	c.Title = strings.TrimSpace(c.Title)
	if c.Title == "" || len([]rune(c.Title)) > 120 {
		return c, invalid("title is required (up to 120 characters)")
	}
	chat, ok := telegram.NormalizeChat(c.ChatID)
	if !ok {
		return c, invalid("channel must be @username, a t.me link or a numeric chat id")
	}
	c.ChatID = chat
	c.TokenEnc = nil
	if tok := strings.TrimSpace(c.BotToken); tok != "" {
		if !telegram.ValidToken(tok) {
			return c, invalid("bot token has an invalid format (expected 123456:ABC...)")
		}
		enc, err := s.sealer.Seal(tok)
		if err != nil {
			return c, fmt.Errorf("%w: %s", domain.ErrConflict, err)
		}
		c.TokenEnc = enc
	} else if c.ID == 0 {
		return c, invalid("bot token is required")
	}
	if c.ID == 0 {
		created, err := s.store.CreateChannel(ctx, c)
		if err != nil {
			return created, err
		}
		return s.VerifyChannel(ctx, created.ID)
	}
	updated, err := s.store.UpdateChannel(ctx, c)
	if err != nil {
		return updated, err
	}
	if updated.Status == "unverified" {
		return s.VerifyChannel(ctx, updated.ID)
	}
	return updated, nil
}

func (s *Service) DeleteChannel(ctx context.Context, id int64) error {
	return s.store.DeleteChannel(ctx, id)
}

// VerifyChannel checks the token, that the chat exists, and that the bot may post there.
// Failures are stored on the channel rather than returned, so the panel shows the reason.
func (s *Service) VerifyChannel(ctx context.Context, id int64) (domain.Channel, error) {
	ch, err := s.store.Channel(ctx, id)
	if err != nil {
		return ch, err
	}
	token, err := s.channelToken(ch)
	if err != nil {
		_ = s.store.SetChannelVerification(ctx, id, "error", "", "", err.Error())
		return s.store.Channel(ctx, id)
	}
	record := func(status, bot, chat, msg string) (domain.Channel, error) {
		if err := s.store.SetChannelVerification(ctx, id, status, bot, chat, msg); err != nil {
			return ch, err
		}
		return s.store.Channel(ctx, id)
	}
	me, err := s.tg.GetMe(ctx, token)
	if err != nil {
		return record("error", "", "", "bot token rejected: "+err.Error())
	}
	chat, err := s.tg.GetChat(ctx, token, ch.ChatID)
	if err != nil {
		return record("error", me.Username, "", "chat not found or bot has no access: "+err.Error())
	}
	member, err := s.tg.GetMember(ctx, token, ch.ChatID, me.ID)
	if err != nil {
		return record("error", me.Username, chat.Title, "cannot read the bot's rights: "+err.Error())
	}
	switch {
	case member.Status == "creator":
	case member.Status == "administrator" && (chat.Type != "channel" || member.CanPostMessage == nil || *member.CanPostMessage):
	case member.Status == "member" && chat.Type != "channel":
	default:
		return record("error", me.Username, chat.Title, "add the bot to the channel as an administrator with permission to post messages")
	}
	return record("ok", me.Username, chat.Title, "")
}

// TestChannel sends a short message so the owner can see where posts will appear.
func (s *Service) TestChannel(ctx context.Context, id int64) error {
	ch, err := s.store.Channel(ctx, id)
	if err != nil {
		return err
	}
	token, err := s.channelToken(ch)
	if err != nil {
		return err
	}
	if _, err = s.tg.SendMessage(ctx, token, ch.ChatID, "✅ Terex News: тестовое сообщение. Канал подключён.", false); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrConflict, err)
	}
	return nil
}

// ---- flows ----

func (s *Service) ListFlows(ctx context.Context) ([]domain.Flow, error) { return s.store.Flows(ctx) }

func (s *Service) GetFlow(ctx context.Context, id int64) (domain.Flow, error) {
	return s.store.Flow(ctx, id)
}

func (s *Service) DeleteFlow(ctx context.Context, id int64) error { return s.store.DeleteFlow(ctx, id) }

func ApplyFlowDefaults(f *domain.Flow) {
	if f.Language == "" {
		f.Language = "ru"
	}
	if f.ApproveMode == "" {
		f.ApproveMode = domain.ApproveManual
	}
	if f.Timezone == "" {
		f.Timezone = "Europe/Moscow"
	}
	if f.PostsPerDay == 0 {
		f.PostsPerDay = 3
	}
	if f.WindowEnd == 0 {
		f.WindowStart, f.WindowEnd = 9, 22
	}
	if f.MinIntervalMin == 0 {
		f.MinIntervalMin = 60
	}
	if f.MinSources == 0 {
		f.MinSources = 1
	}
	if f.MinRelevance == 0 {
		f.MinRelevance = 0.25
	}
	if f.WindowHours == 0 {
		f.WindowHours = 24
	}
	if f.MaxPending == 0 {
		f.MaxPending = 5
	}
	if f.MaxChars == 0 {
		f.MaxChars = 900
	}
}

func validateFlow(f *domain.Flow) error {
	f.Name = strings.TrimSpace(f.Name)
	f.Topic = strings.TrimSpace(f.Topic)
	f.StylePrompt = strings.TrimSpace(f.StylePrompt)
	f.Model = strings.TrimSpace(f.Model)
	ApplyFlowDefaults(f)
	switch {
	case f.Name == "" || len([]rune(f.Name)) > 120:
		return invalid("name is required (up to 120 characters)")
	case len([]rune(f.Topic)) > 500:
		return invalid("topic is too long (500 characters max)")
	case len([]rune(f.StylePrompt)) > 2000:
		return invalid("style notes are too long (2000 characters max)")
	case f.Language != "ru" && f.Language != "en":
		return invalid("language must be ru or en")
	case f.ApproveMode != domain.ApproveManual && f.ApproveMode != domain.ApproveAuto:
		return invalid("approve mode must be by_approve or auto")
	case f.PostsPerDay < 1 || f.PostsPerDay > 48:
		return invalid("posts per day must be 1-48")
	case f.WindowStart < 0 || f.WindowEnd > 24 || f.WindowStart >= f.WindowEnd:
		return invalid("active hours must satisfy 0 <= start < end <= 24")
	case f.MinIntervalMin < 0 || f.MinIntervalMin > 1440:
		return invalid("minimum interval must be 0-1440 minutes")
	case f.MinSources < 1 || f.MinSources > 20:
		return invalid("minimum sources must be 1-20")
	case f.MinRelevance < 0 || f.MinRelevance > 1:
		return invalid("minimum relevance must be between 0 and 1")
	case f.WindowHours < 1 || f.WindowHours > 168:
		return invalid("story window must be 1-168 hours")
	case f.MaxPending < 1 || f.MaxPending > 50:
		return invalid("maximum drafts awaiting review must be 1-50")
	case f.MaxChars < 200 || f.MaxChars > 3500:
		return invalid("post length must be 200-3500 characters")
	case len(f.Model) > 100 || strings.ContainsAny(f.Model, " \t\r\n"):
		return invalid("model id is invalid")
	}
	if _, err := time.LoadLocation(f.Timezone); err != nil {
		return invalid("unknown timezone %q", f.Timezone)
	}
	if f.SourceIDs == nil {
		f.SourceIDs = []int64{}
	}
	if len(f.SourceIDs) > 200 {
		return invalid("too many sources")
	}
	if f.Enabled {
		if f.ChannelID == nil {
			return invalid("choose a Telegram channel before enabling the flow")
		}
		if len(f.SourceIDs) == 0 {
			return invalid("choose at least one source before enabling the flow")
		}
	}
	return nil
}

func (s *Service) SaveFlow(ctx context.Context, f domain.Flow) (domain.Flow, error) {
	if err := validateFlow(&f); err != nil {
		return f, err
	}
	return s.store.SaveFlow(ctx, f)
}

// GenerateNow drafts a post immediately, ignoring the schedule (but not the channel/LLM checks).
func (s *Service) GenerateNow(ctx context.Context, id int64) (*domain.Post, error) {
	f, err := s.store.Flow(ctx, id)
	if err != nil {
		return nil, err
	}
	s.pipeMu.Lock()
	defer s.pipeMu.Unlock()
	if err = s.assign(ctx, f); err != nil {
		return nil, err
	}
	p, err := s.generate(ctx, f, true)
	if err != nil {
		_ = s.store.SetFlowResult(ctx, f.ID, false, err.Error())
	}
	return p, err
}

func (s *Service) FlowCandidates(ctx context.Context, id int64) ([]domain.Cluster, error) {
	f, err := s.store.Flow(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.Candidates(ctx, f)
}

func (s *Service) RecentArticles(ctx context.Context, flowID int64, limit int) ([]domain.Article, error) {
	return s.store.RecentArticles(ctx, flowID, limit)
}

// ---- posts ----

func (s *Service) ListPosts(ctx context.Context, flowID int64, status string, limit int) ([]domain.Post, error) {
	return s.store.ListPosts(ctx, flowID, status, limit)
}

func (s *Service) GetPost(ctx context.Context, id int64) (domain.Post, error) {
	return s.store.Post(ctx, id)
}

type PostEdit struct {
	Title         *string    `json:"title"`
	Text          *string    `json:"text"`
	ScheduledAt   *time.Time `json:"scheduled_at"`
	ClearSchedule bool       `json:"clear_schedule"`
}

func (s *Service) EditPost(ctx context.Context, id int64, e PostEdit) (domain.Post, error) {
	p, err := s.store.Post(ctx, id)
	if err != nil {
		return p, err
	}
	title, text := p.Title, p.Text
	if e.Title != nil {
		title = strings.TrimSpace(*e.Title)
	}
	if e.Text != nil {
		text = strings.TrimSpace(*e.Text)
	}
	if text == "" || len([]rune(text)) > 3800 || len([]rune(title)) > 200 {
		return p, invalid("text is required (up to 3800 characters), title up to 200")
	}
	if err = s.store.EditPost(ctx, id, title, text, e.ScheduledAt, e.ScheduledAt != nil || e.ClearSchedule); err != nil {
		return p, err
	}
	return s.store.Post(ctx, id)
}

// Approve queues a draft; without a time it goes out immediately.
func (s *Service) ApprovePost(ctx context.Context, id int64, at *time.Time) (domain.Post, error) {
	when := s.now()
	if at != nil {
		when = *at
	}
	if err := s.store.Transition(ctx, id, []string{domain.PostPending, domain.PostFailed}, domain.PostApproved, &when); err != nil {
		return domain.Post{}, err
	}
	if !when.After(s.now()) {
		if err := s.publishDue(ctx); err != nil {
			s.log.Warn("publish after approval failed", "err", err)
		}
	}
	return s.store.Post(ctx, id)
}

func (s *Service) RejectPost(ctx context.Context, id int64) (domain.Post, error) {
	if err := s.store.Transition(ctx, id, []string{domain.PostPending, domain.PostApproved, domain.PostFailed}, domain.PostRejected, nil); err != nil {
		return domain.Post{}, err
	}
	return s.store.Post(ctx, id)
}

func (s *Service) ListStatuses() []string {
	return []string{domain.PostPending, domain.PostApproved, domain.PostPublished, domain.PostFailed, domain.PostRejected, domain.PostExpired}
}
