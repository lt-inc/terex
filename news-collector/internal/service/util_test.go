package service

import (
	"encoding/base64"
	"strings"
	"testing"

	"terex/news-collector/internal/domain"
)

func TestSimhashNearDuplicates(t *testing.T) {
	base := "Центральный банк сегодня объявил о повышении ключевой ставки на один процентный пункт, сообщили в пресс-службе регулятора. Решение принято на заседании совета директоров, аналитики ожидали такого шага."
	tweaked := base + " Подробности уточняются."
	other := "В Москве открылась новая станция метро, которая соединит северные районы города с центром. Пассажиры смогут сэкономить до двадцати минут на дороге, заявил мэр во время церемонии открытия."
	if d := hamming(simhash(base), simhash(tweaked)); d > 8 {
		t.Errorf("near-identical texts differ in %d bits", d)
	}
	if d := hamming(simhash(base), simhash(other)); d < 12 {
		t.Errorf("unrelated texts differ in only %d bits", d)
	}
	if simhash("two words") != 0 {
		t.Error("texts shorter than a shingle must not produce a fingerprint")
	}
}

func TestCosineAndMean(t *testing.T) {
	a, b := []float32{1, 0}, []float32{0, 1}
	if cosine(a, a) < 0.999 || cosine(a, b) != 0 || cosine(a, []float32{1}) != 0 || cosine(nil, nil) != 0 {
		t.Fatal("cosine edge cases are wrong")
	}
	m := meanUpdate([]float32{1, 0}, 1, []float32{0, 1})
	if m[0] != 0.5 || m[1] != 0.5 {
		t.Fatalf("mean = %v", m)
	}
}

func TestNormalizeURL(t *testing.T) {
	a := normalizeURL("https://Example.com/news/1/?utm_source=x&id=7#top")
	b := normalizeURL("https://example.com/news/1?id=7")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if urlHash("https://example.com/a") == urlHash("https://example.com/b") {
		t.Fatal("different URLs share a hash")
	}
}

func TestDecodeGNewsLink(t *testing.T) {
	payload := append([]byte{0x08, 0x13, 0x22, 0x1a}, []byte("https://example.com/story/1")...)
	payload = append(payload, 0xd2, 0x01, 0x00)
	link := "https://news.google.com/rss/articles/" + base64.RawURLEncoding.EncodeToString(payload) + "?oc=5"
	if got := decodeGNewsLink(link); got != "https://example.com/story/1" {
		t.Fatalf("decoded %q", got)
	}
	if decodeGNewsLink("https://news.google.com/rss/articles/CBMiAU_opaque") != "" {
		t.Error("an opaque id must not decode")
	}
	if decodeGNewsLink("https://example.com/x") != "" {
		t.Error("non-Google links are not decoded")
	}
}

func TestSealer(t *testing.T) {
	if s, err := NewSealer(""); s != nil || err != nil {
		t.Fatal("an empty key means no sealer")
	}
	if _, err := NewSealer("short"); err == nil {
		t.Fatal("a bad key must be rejected")
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := s.Seal("123456:secret-token")
	if err != nil || strings.Contains(string(enc), "secret-token") {
		t.Fatal("token must be encrypted")
	}
	if got, err := s.Open(enc); err != nil || got != "123456:secret-token" {
		t.Fatalf("roundtrip: %q %v", got, err)
	}
	other, _ := NewSealer(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 32))))
	if _, err := other.Open(enc); err == nil {
		t.Fatal("a different key must not decrypt")
	}
	var none *Sealer
	if _, err := none.Seal("x"); err != ErrNoSecretKey {
		t.Fatal("missing key must be reported")
	}
}

func TestRenderHTMLEscapesAndFits(t *testing.T) {
	p := domain.Post{Title: "A & B <c>", Text: "x < y & z\n\nsecond", Links: []domain.Link{{Title: "Out<let>", URL: `https://e.com/?a=1&b="2"`}}}
	got := RenderHTML(p, RenderOptions{Lang: "en"})
	for _, want := range []string{"<b>A &amp; B &lt;c&gt;</b>", "x &lt; y &amp; z", `href="https://e.com/?a=1&amp;b=&#34;2&#34;"`, "Sources: ", "Out&lt;let&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	long := domain.Post{Title: "t", Text: strings.Repeat("слово ", 2000)}
	if n := len([]rune(RenderHTML(long, RenderOptions{Lang: "ru"}))); n > telegramLimit {
		t.Errorf("message has %d characters, Telegram allows %d", n, telegramLimit)
	}
	if !strings.Contains(RenderHTML(domain.Post{Text: "a", Links: []domain.Link{{Title: "x", URL: "https://x.y"}}}, RenderOptions{Lang: "ru"}), "Источники: ") {
		t.Error("Russian label expected")
	}
}

func TestTrimToLimit(t *testing.T) {
	text := "Первое предложение. Второе предложение. Третье предложение, которое длиннее."
	got := trimToLimit(text, 45)
	if got != "Первое предложение. Второе предложение." {
		t.Errorf("got %q", got)
	}
	if trimToLimit("short", 100) != "short" {
		t.Error("short text must stay")
	}
}

func TestValidateFlow(t *testing.T) {
	ok := domain.Flow{Name: "Crypto"}
	if err := validateFlow(&ok); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	if ok.PostsPerDay != 3 || ok.WindowStart != 9 || ok.WindowEnd != 22 || ok.ApproveMode != domain.ApproveManual {
		t.Fatalf("defaults not applied: %+v", ok)
	}
	for name, mutate := range map[string]func(*domain.Flow){
		"enable without channel": func(f *domain.Flow) { f.Enabled = true; f.SourceIDs = []int64{1} },
		"enable without sources": func(f *domain.Flow) { f.Enabled = true; ch := int64(1); f.ChannelID = &ch },
		"bad window":             func(f *domain.Flow) { f.WindowStart, f.WindowEnd = 20, 10 },
		"bad timezone":           func(f *domain.Flow) { f.Timezone = "Mars/Base" },
		"bad language":           func(f *domain.Flow) { f.Language = "de" },
		"posts per day":          func(f *domain.Flow) { f.PostsPerDay = 99 },
		"model with spaces":      func(f *domain.Flow) { f.Model = "a b" },
	} {
		f := domain.Flow{Name: "x"}
		mutate(&f)
		if err := validateFlow(&f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEffectiveIntervalSpreadsPosts(t *testing.T) {
	f := domain.Flow{PostsPerDay: 3, WindowStart: 9, WindowEnd: 22, MinIntervalMin: 60}
	if got := effectiveInterval(f).Minutes(); got < 190 || got > 200 {
		t.Errorf("interval = %.0f min", got)
	}
	f.PostsPerDay = 48
	if got := effectiveInterval(f).Minutes(); got != 60 {
		t.Errorf("the configured minimum must win, got %.0f", got)
	}
}

func TestDetectLang(t *testing.T) {
	if detectLang("Курс рубля вырос") != "ru" || detectLang("The ruble rose") != "en" {
		t.Fatal("language detection failed")
	}
}

func TestMarkupToHTML(t *testing.T) {
	in := "Лид с **важным фактом** и __термином__.\n\n- первый <пункт>\n* второй пункт\n• третий\n\n> цитата **жирная**\n> вторая строка\n\nНезакрытый **маркер и & амперсанд"
	got := markupToHTML(in)
	for _, want := range []string{
		"Лид с <b>важным фактом</b> и <i>термином</i>.",
		"• первый &lt;пункт&gt;", "• второй пункт", "• третий",
		"<blockquote>цитата <b>жирная</b>\nвторая строка</blockquote>",
		"Незакрытый маркер и &amp; амперсанд",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**") || strings.Contains(got, "__") || strings.Contains(got, "<script") {
		t.Errorf("stray markers or injected tags:\n%s", got)
	}
	if injected := markupToHTML("<script>alert(1)</script> <b>x</b>"); strings.Contains(injected, "<script>") || strings.Contains(injected, "<b>x</b>") {
		t.Errorf("model or editor HTML must be escaped: %s", injected)
	}
}

func TestRenderLayout(t *testing.T) {
	p := domain.Post{Title: "Заголовок", Emoji: "🔥", Text: "Лид с **фактом**.", Hashtags: "#Крипто #Биткоин",
		Links:       []domain.Link{{Title: "РБК", URL: "https://rbc.ru/1"}},
		ImageSource: "pexels", ImageCredit: "Ann", ImageCreditURL: "https://www.pexels.com/photo/1/"}
	got := RenderHTML(p, RenderOptions{Lang: "ru", Footer: "Подписаться: @news & co"})
	order := []string{"🔥 <b>Заголовок</b>", "Лид с <b>фактом</b>.", "#Крипто #Биткоин", "Источники: <a", "Фото: <a", "/ Pexels", "Подписаться: @news &amp; co"}
	last := -1
	for _, part := range order {
		i := strings.Index(got, part)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order in:\n%s", part, got)
		}
		last = i
	}
	if plainLength("<b>a &amp; b</b> <a href=\"x\">c</a>") != 7 {
		t.Error("plainLength must count rendered characters only")
	}
}

func TestCleaners(t *testing.T) {
	if got := cleanTitle("## **Заголовок**\n"); got != "Заголовок" {
		t.Errorf("title: %q", got)
	}
	if got := cleanBody("# Раздел\n* пункт\n`код` и **жирное**"); got != "Раздел\n- пункт\nкод и **жирное**" {
		t.Errorf("body: %q", got)
	}
	if cleanEmoji("🔥") != "🔥" || cleanEmoji("fire") != "" || cleanEmoji(" 🔥 📈 ") != "🔥📈" {
		t.Error("emoji must be symbols only")
	}
	if got := cleanHashtags([]string{"#Биткоин", "крипто рынок", "биткоин", "", "a-b", "x", "y", "z"}); got != "#Биткоин #крипторынок #ab #x" {
		t.Errorf("hashtags: %q", got)
	}
}
