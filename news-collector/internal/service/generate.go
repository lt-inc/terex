package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/news-collector/internal/repository/llm"
)

// effectiveInterval spreads a day's posts over the active window instead of bursting them.
func effectiveInterval(f domain.Flow) time.Duration {
	interval := time.Duration(f.MinIntervalMin) * time.Minute
	span := time.Duration(f.WindowEnd-f.WindowStart) * time.Hour
	if f.PostsPerDay > 0 && span > 0 {
		if spread := span / time.Duration(f.PostsPerDay) * 3 / 4; spread > interval {
			interval = spread
		}
	}
	return interval
}

// due reports whether the schedule allows a new draft right now.
func (s *Service) due(ctx context.Context, f domain.Flow) (bool, error) {
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := s.now().In(loc)
	if h := local.Hour(); h < f.WindowStart || h >= f.WindowEnd {
		return false, nil
	}
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	n, err := s.store.CountPosts(ctx, f.ID, dayStart)
	if err != nil || n >= f.PostsPerDay {
		return false, err
	}
	if f.ApproveMode == domain.ApproveManual {
		pending, err := s.store.CountPending(ctx, f.ID)
		if err != nil || pending >= f.MaxPending {
			return false, err
		}
	}
	last, err := s.store.LastPostCreated(ctx, f.ID)
	if err != nil {
		return false, err
	}
	if last != nil && s.now().Sub(*last) < effectiveInterval(f) {
		return false, nil
	}
	return true, nil
}

var errNothing = errors.New("no suitable story")

// generate drafts one post for the flow. With force the schedule limits are ignored (used by
// "generate now"); readiness checks always apply. It returns nil, nil when nothing is due.
func (s *Service) generate(ctx context.Context, f domain.Flow, force bool) (*domain.Post, error) {
	if !s.llm.Ready() {
		return nil, llm.ErrNotConfigured
	}
	if f.ChannelID == nil {
		return nil, fmt.Errorf("%w: the flow has no Telegram channel", domain.ErrInvalid)
	}
	if !force {
		ok, err := s.due(ctx, f)
		if err != nil || !ok {
			return nil, err
		}
	}
	if f.Topic != "" && len(f.TopicEmbedding) == 0 {
		if err := s.assign(ctx, f); err != nil {
			return nil, err
		}
	}
	candidates, err := s.Candidates(ctx, f)
	if err != nil {
		return nil, err
	}
	used, err := s.store.RecentUsedClusters(ctx, f.ID, s.now().Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	tried := 0
	for _, c := range candidates {
		if c.Sources < f.MinSources {
			continue
		}
		if coveredBy(c, used, s.cfg.CoveredThreshold) {
			metrics.StoriesSkipped.WithLabelValues(flowLabel(f.ID), "covered").Inc()
			_ = s.store.SetClusterStatus(ctx, c.ID, "skipped")
			continue
		}
		if tried++; tried > 3 {
			break
		}
		post, err := s.draft(ctx, f, c)
		if errors.Is(err, errNothing) {
			metrics.StoriesSkipped.WithLabelValues(flowLabel(f.ID), "model_skip").Inc()
			_ = s.store.SetClusterStatus(ctx, c.ID, "skipped")
			continue
		}
		if err != nil {
			return nil, err
		}
		return post, nil
	}
	if force {
		return nil, fmt.Errorf("%w: no story with enough sources is available yet", domain.ErrConflict)
	}
	return nil, nil
}

func coveredBy(c domain.Cluster, used []domain.Cluster, threshold float64) bool {
	for _, u := range used {
		if cosine(c.Centroid, u.Centroid) >= threshold {
			return true
		}
	}
	return false
}

type draft struct {
	Skip       bool     `json:"skip"`
	Reason     string   `json:"reason"`
	Title      string   `json:"title"`
	Emoji      string   `json:"emoji"`
	Text       string   `json:"text"`
	Hashtags   []string `json:"hashtags"`
	ImageQuery string   `json:"image_query"`
}

func (s *Service) draft(ctx context.Context, f domain.Flow, c domain.Cluster) (*domain.Post, error) {
	arts, err := s.store.ClusterArticles(ctx, c.ID, 6)
	if err != nil {
		return nil, err
	}
	if len(arts) == 0 {
		return nil, errNothing
	}
	var out draft
	if err = s.llm.JSON(ctx, f.Model, buildPrompt(f, arts), &out); err != nil {
		return nil, err
	}
	out.Title = cleanTitle(out.Title)
	out.Text = cleanBody(out.Text)
	if out.Skip || out.Text == "" {
		s.log.Info("model skipped a story", "flow", f.ID, "cluster", c.ID, "reason", out.Reason)
		return nil, errNothing
	}
	out.Title = truncateRunes(out.Title, 120)
	out.Text = trimToLimit(out.Text, f.MaxChars)

	post := domain.Post{FlowID: f.ID, ClusterID: &c.ID, Title: out.Title, Text: out.Text, Links: []domain.Link{}, Status: domain.PostPending, Score: score(c, s.now())}
	if f.UseEmoji {
		post.Emoji = cleanEmoji(out.Emoji)
	}
	if f.UseHashtags {
		post.Hashtags = cleanHashtags(out.Hashtags)
	}
	post.ImageQuery = truncateRunes(strings.TrimSpace(out.ImageQuery), 80)
	if img := s.pickImage(ctx, f, arts, post.ImageQuery); img.URL != "" {
		post.ImageURL, post.ImageSource, post.ImageCredit, post.ImageCreditURL = img.URL, img.Source, img.Credit, img.CreditURL
	}
	if f.IncludeLink {
		seen := map[string]bool{}
		for _, a := range arts {
			name := a.Publisher
			if name == "" {
				name = a.SourceName
			}
			if seen[name] || len(post.Links) >= 3 {
				continue
			}
			seen[name] = true
			post.Links = append(post.Links, domain.Link{Title: name, URL: a.URL})
		}
	}
	if f.ApproveMode == domain.ApproveAuto {
		now := s.now()
		post.Status, post.ScheduledAt = domain.PostApproved, &now
	}
	id, err := s.store.CreatePost(ctx, post)
	if err != nil {
		return nil, err
	}
	if err = s.store.SetClusterStatus(ctx, c.ID, "used"); err != nil {
		return nil, err
	}
	_ = s.store.SetFlowResult(ctx, f.ID, true, "")
	metrics.PostsDrafted.WithLabelValues(flowLabel(f.ID), f.ApproveMode).Inc()
	s.log.Info("post drafted", "flow", f.ID, "post", id, "cluster", c.ID, "status", post.Status, "image", post.ImageSource)
	created, err := s.store.Post(ctx, id)
	return &created, err
}

type pickedImage struct{ URL, Source, Credit, CreditURL string }

// pickImage applies the flow's picture policy: the story's own picture, a stock photo, or both in order.
// A failure only means "no picture"; it never blocks the post.
func (s *Service) pickImage(ctx context.Context, f domain.Flow, arts []domain.Article, query string) pickedImage {
	none := func() pickedImage { metrics.ImagesSelected.WithLabelValues("none").Inc(); return pickedImage{} }
	if f.ImageMode == domain.ImageNone || f.ImageMode == "" {
		return pickedImage{}
	}
	if f.ImageMode == domain.ImageArticle || f.ImageMode == domain.ImageAuto {
		tried := 0
		for _, a := range arts {
			if a.ImageURL == "" || tried >= 4 {
				continue
			}
			tried++
			if err := s.checkImage(ctx, a.ImageURL); err != nil {
				metrics.ImageLookups.WithLabelValues("article_check", "error").Inc()
				continue
			}
			metrics.ImageLookups.WithLabelValues("article_check", "ok").Inc()
			metrics.ImagesSelected.WithLabelValues("article").Inc()
			return pickedImage{URL: a.ImageURL, Source: "article"}
		}
	}
	if f.ImageMode == domain.ImageWeb || f.ImageMode == domain.ImageAuto {
		q := query
		if q == "" {
			q = strings.TrimSpace(f.Topic)
		}
		for _, prov := range s.stock {
			if !prov.Ready() || q == "" {
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			photos, err := prov.Search(pctx, q, f.Language, 5)
			cancel()
			switch {
			case err != nil:
				metrics.ImageLookups.WithLabelValues(prov.Name(), "error").Inc()
				s.log.Warn("stock photo search failed", "flow", f.ID, "provider", prov.Name(), "err", err)
			case len(photos) > 0:
				metrics.ImageLookups.WithLabelValues(prov.Name(), "ok").Inc()
				metrics.ImagesSelected.WithLabelValues(prov.Name()).Inc()
				return pickedImage{URL: photos[0].URL, Source: prov.Name(), Credit: photos[0].Credit, CreditURL: photos[0].CreditURL}
			default:
				metrics.ImageLookups.WithLabelValues(prov.Name(), "empty").Inc()
			}
		}
	}
	return none()
}

func (s *Service) checkImage(ctx context.Context, url string) error {
	if s.checker == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.checker.Check(cctx, url)
}

func buildPrompt(f domain.Flow, arts []domain.Article) []llm.Message {
	language := "Russian"
	if f.Language == "en" {
		language = "English"
	}
	topic := f.Topic
	if topic == "" {
		topic = "general news"
	}
	wantImage := f.ImageMode == domain.ImageWeb || f.ImageMode == domain.ImageAuto
	var sys strings.Builder
	fmt.Fprintf(&sys, "You are the editor of a Telegram news channel about: %s.\n", topic)
	sys.WriteString("Write ONE post about the story that the source articles below describe.\n\nContent rules:\n")
	fmt.Fprintf(&sys, "- Write in %s. Translate from the sources when they use another language.\n", language)
	sys.WriteString("- Use ONLY facts stated in the sources. Never invent numbers, quotes, names, dates or causes. If sources disagree, leave the disputed detail out or say they differ.\n")
	sys.WriteString("- Neutral, clear tone. No clickbait.\n")
	fmt.Fprintf(&sys, "- Length: a short headline (up to 90 characters) and a body of at most %d characters in total.\n", f.MaxChars)
	sys.WriteString("- If the sources are not really about the channel's topic or hold too little factual content for a useful post, answer {\"skip\": true, \"reason\": \"...\"}.\n")
	sys.WriteString("\nLayout rules (make the post pleasant to read):\n")
	sys.WriteString("- Start the body with a lead of 1-2 sentences that carries the main fact.\n")
	sys.WriteString("- Separate paragraphs with a blank line; no paragraph longer than 3 sentences.\n")
	sys.WriteString("- Mark 2-4 key facts (names, numbers, decisions) with **bold**. Use __italic__ rarely, for a term or a caveat.\n")
	sys.WriteString("- When there are three or more parallel facts, list them as lines starting with \"- \".\n")
	sys.WriteString("- Put a direct quotation on its own line starting with \"> \", only if it appears word for word in the sources.\n")
	sys.WriteString("- The ONLY allowed markup is **bold**, __italic__, \"- \" bullets and \"> \" quotes. No HTML, no # headings, no links (source links are added automatically).\n")
	if f.UseEmoji {
		sys.WriteString("- Put one fitting emoji in the \"emoji\" field (it is shown before the headline). At most two more emoji inside the text, only where they help.\n")
	} else {
		sys.WriteString("- Do not use emoji anywhere; leave \"emoji\" empty.\n")
	}
	if f.UseHashtags {
		sys.WriteString("- Give 2-4 hashtags in the \"hashtags\" array: single words in the post language without spaces or the # sign.\n")
	} else {
		sys.WriteString("- Leave \"hashtags\" as an empty array.\n")
	}
	if wantImage {
		sys.WriteString("- In \"image_query\" give 2-4 English words for a stock-photo search that illustrates the story neutrally (a scene or object, no personal names, no text in the picture).\n")
	} else {
		sys.WriteString("- Leave \"image_query\" empty.\n")
	}
	if strings.TrimSpace(f.StylePrompt) != "" {
		fmt.Fprintf(&sys, "\nChannel style notes from the owner (follow them unless they conflict with the rules above):\n%s\n", strings.TrimSpace(f.StylePrompt))
	}
	sys.WriteString("\nAnswer with JSON only: {\"skip\": false, \"emoji\": \"...\", \"title\": \"...\", \"text\": \"...\", \"hashtags\": [\"...\"], \"image_query\": \"...\"}")

	var user strings.Builder
	budget := 9000
	for i, a := range arts {
		body := a.Text
		if body == "" {
			body = a.Summary
		}
		body = truncateRunes(body, min(2500, max(400, budget)))
		budget -= len([]rune(body))
		name := a.Publisher
		if name == "" {
			name = a.SourceName
		}
		fmt.Fprintf(&user, "[%d] %s | %s | %s\n%s\n\n", i+1, name, a.PublishedAt.UTC().Format("2006-01-02 15:04"), a.Title, body)
		if budget <= 0 {
			break
		}
	}
	return []llm.Message{{Role: "system", Content: sys.String()}, {Role: "user", Content: user.String()}}
}

var hashtagWord = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// cleanTitle strips any markup: the headline is rendered bold by the layout itself.
func cleanTitle(s string) string {
	s = strings.NewReplacer("**", "", "__", "", "`", "", "\n", " ", "\r", " ").Replace(s)
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "# "))
}

// cleanBody keeps the allowed light markup and removes what a model sometimes adds anyway:
// headings, code ticks and "* " bullets (normalised to "- ").
func cleanBody(s string) string {
	s = strings.ReplaceAll(s, "`", "")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, " \t")
		t := strings.TrimLeft(l, " \t")
		switch {
		case strings.HasPrefix(t, "#"):
			t = strings.TrimSpace(strings.TrimLeft(t, "#"))
		case strings.HasPrefix(t, "* "):
			t = "- " + strings.TrimPrefix(t, "* ")
		}
		lines[i] = t
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// cleanEmoji keeps a short emoji string: no letters, no spaces, at most a few symbols.
func cleanEmoji(s string) string {
	s = strings.TrimSpace(s)
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return ""
		}
	}
	return truncateRunes(strings.Join(strings.Fields(s), ""), 8)
}

// cleanHashtags turns model output into "#tag #other": letters, digits and underscores only.
func cleanHashtags(tags []string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = hashtagWord.ReplaceAllString(strings.TrimLeft(strings.TrimSpace(t), "#"), "")
		if t == "" || len([]rune(t)) > 30 || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, "#"+t)
		if len(out) == 4 {
			break
		}
	}
	return strings.Join(out, " ")
}

func flowLabel(id int64) string { return strconv.FormatInt(id, 10) }
