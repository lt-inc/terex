package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/news-collector/internal/repository/telegram"
)

const telegramLimit = 4096

// RenderHTML builds the Telegram message: bold headline, text, and source links.
func RenderHTML(p domain.Post, lang string) string {
	var b strings.Builder
	if p.Title != "" {
		b.WriteString("<b>" + html.EscapeString(p.Title) + "</b>\n\n")
	}
	tail := ""
	if len(p.Links) > 0 {
		label := "Источники: "
		if lang == "en" {
			label = "Sources: "
		}
		parts := make([]string, 0, len(p.Links))
		for _, l := range p.Links {
			parts = append(parts, `<a href="`+html.EscapeString(l.URL)+`">`+html.EscapeString(l.Title)+`</a>`)
		}
		tail = "\n\n" + label + strings.Join(parts, " · ")
	}
	// Telegram counts characters of the rendered text, not markup; stay well under the limit.
	room := telegramLimit - len([]rune(p.Title)) - len([]rune(tail)) - 200
	b.WriteString(html.EscapeString(trimToLimit(p.Text, max(room, 200))))
	b.WriteString(tail)
	return b.String()
}

// publishDue sends approved posts whose time has come.
func (s *Service) publishDue(ctx context.Context) error {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	posts, err := s.store.DuePosts(ctx, s.now(), 10)
	if err != nil {
		return err
	}
	for _, p := range posts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		claimed, err := s.store.ClaimPost(ctx, p.ID)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		s.send(ctx, p)
	}
	return nil
}

func (s *Service) send(ctx context.Context, p domain.Post) {
	fail := func(msg string, retry *time.Time) {
		outcome := "failed"
		if retry != nil {
			outcome = "retry"
		}
		metrics.PostSendFailures.WithLabelValues(flowLabel(p.FlowID), outcome).Inc()
		if err := s.store.MarkSendFailed(ctx, p.ID, msg, retry); err != nil {
			s.log.Error("post state update failed", "post", p.ID, "err", err)
		}
		s.log.Warn("post not published", "post", p.ID, "reason", msg, "retry", retry != nil)
	}
	flow, err := s.store.Flow(ctx, p.FlowID)
	if err != nil || flow.ChannelID == nil {
		fail("the flow has no Telegram channel", nil)
		return
	}
	ch, err := s.store.Channel(ctx, *flow.ChannelID)
	if err != nil {
		fail("Telegram channel not found", nil)
		return
	}
	token, err := s.sealer.Open(ch.TokenEnc)
	if err != nil {
		fail(err.Error(), nil)
		return
	}
	id, err := s.tg.SendMessage(ctx, token, ch.ChatID, RenderHTML(p, flow.Language), false)
	if err == nil {
		if err = s.store.MarkPublished(ctx, p.ID, id); err != nil {
			s.log.Error("published post could not be marked", "post", p.ID, "message", id, "err", err)
		}
		metrics.PostsPublished.WithLabelValues(flowLabel(p.FlowID)).Inc()
		metrics.PostPublishDelay.Observe(s.now().Sub(p.CreatedAt).Seconds())
		s.log.Info("post published", "post", p.ID, "flow", p.FlowID, "message", id)
		return
	}
	var tgErr *telegram.Error
	if errors.As(err, &tgErr) {
		if tgErr.Code == 401 || tgErr.Code == 403 {
			_ = s.store.SetChannelVerification(ctx, ch.ID, "error", ch.BotUsername, ch.ChatTitle, tgErr.Message)
		}
		if tgErr.RetryAfter > 0 {
			at := s.now().Add(tgErr.RetryAfter + time.Second)
			fail(tgErr.Error(), &at)
			return
		}
		if tgErr.Permanent() {
			fail(tgErr.Error(), nil)
			return
		}
	}
	if p.Attempts < 2 {
		at := s.now().Add(time.Duration(1<<p.Attempts) * time.Minute)
		fail(err.Error(), &at)
		return
	}
	fail(err.Error(), nil)
}

func (s *Service) channelToken(ch domain.Channel) (string, error) {
	if len(ch.TokenEnc) == 0 {
		return "", fmt.Errorf("%w: the channel has no bot token", domain.ErrInvalid)
	}
	return s.sealer.Open(ch.TokenEnc)
}
