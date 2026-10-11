package twitch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"terex/proto/publicationmetadata"
)

const (
	MetadataSchemaVersion = 1
	MetadataContactEmail  = "ceo@legendary-trio.me"
	MaxMetadataHashtags   = 20
)

type MetadataSettings struct {
	ShortIntroTemplate       string   `json:"short_intro_template"`
	CompilationIntroTemplate string   `json:"compilation_intro_template"`
	HashtagMode              string   `json:"hashtag_mode"`
	CustomHashtags           []string `json:"custom_hashtags"`
}

type MetadataSource struct {
	Login          string `json:"login"`
	Language       string `json:"language,omitempty"`
	LanguageSource string `json:"language_source,omitempty"`
}

// MetadataSnapshot freezes the settings and attribution of a concrete release
// version. Only Intro is editable; automatic blocks are built from its sources.
type MetadataSnapshot struct {
	SchemaVersion  int              `json:"schema_version"`
	Format         string           `json:"format"`
	Settings       MetadataSettings `json:"settings"`
	SourceMode     string           `json:"source_mode,omitempty"`
	CategoryName   string           `json:"category_name,omitempty"`
	LanguageFilter string           `json:"language_filter,omitempty"`
	Language       string           `json:"language"`
	Sources        []MetadataSource `json:"sources"`
	Intro          string           `json:"intro"`
	IntroEdited    bool             `json:"intro_edited"`
}

type MetadataInput struct {
	Format         string
	Title          string
	Settings       *MetadataSettings
	SourceMode     string
	CategoryName   string
	LanguageFilter string
	Sources        []Source
	IntroOverride  *string
}

type MetadataResult struct {
	Intro            string           `json:"intro"`
	Description      string           `json:"description"`
	Hashtags         []string         `json:"hashtags"`
	Language         string           `json:"language"`
	Errors           []string         `json:"errors"`
	DescriptionBytes int              `json:"description_bytes"`
	TagCharacters    int              `json:"tag_characters"`
	Snapshot         MetadataSnapshot `json:"snapshot"`
}

func (s MetadataSnapshot) ToInput(title string, sources []Source) MetadataInput {
	settings := s.Settings
	input := MetadataInput{Format: s.Format, Title: title, Settings: &settings, SourceMode: s.SourceMode,
		CategoryName: s.CategoryName, LanguageFilter: s.LanguageFilter, Sources: sources}
	if sources == nil {
		input.Sources = make([]Source, len(s.Sources))
		for i, source := range s.Sources {
			input.Sources[i] = Source{BroadcasterLogin: source.Login, Language: source.Language, LanguageSource: source.LanguageSource}
		}
	}
	if s.IntroEdited {
		intro := s.Intro
		input.IntroOverride = &intro
	}
	return input
}

var metadataVariables = map[string]bool{"title": true, "streamer": true, "streamers": true, "category": true, "clip_count": true}
var metadataPlaceholder = regexp.MustCompile(`\{([^{}]*)\}`)
var twitchLogin = regexp.MustCompile(`^[A-Za-z0-9_]{1,25}$`)

func validateMetadataTemplate(name, template string) []string {
	errors := []string{}
	if size := len([]byte(template)); size > 5000 {
		errors = append(errors, fmt.Sprintf("%s uses %d bytes; the complete YouTube description permits at most 5000 bytes including automatic credits and contact", name, size))
	}
	for _, match := range metadataPlaceholder.FindAllStringSubmatch(template, -1) {
		if !metadataVariables[match[1]] {
			errors = append(errors, fmt.Sprintf("%s: unknown variable {%s}; allowed: {title}, {streamer}, {streamers}, {category}, {clip_count}", name, match[1]))
		}
	}
	if strings.ContainsAny(metadataPlaceholder.ReplaceAllString(template, ""), "{}") {
		errors = append(errors, name+": variables must use matching braces, for example {title}")
	}
	return errors
}

func validHashtag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, ch := range tag {
		if ch != '_' && !unicode.IsLetter(ch) && !unicode.IsNumber(ch) && !unicode.IsMark(ch) {
			return false
		}
	}
	return true
}

func ValidateMetadataSettings(settings *MetadataSettings) []string {
	errors := []string{}
	if settings == nil {
		return errors
	}
	if settings.HashtagMode != "" && settings.HashtagMode != "standard" && settings.HashtagMode != "custom" {
		errors = append(errors, "Hashtag mode must be standard or custom")
	}
	errors = append(errors, validateMetadataTemplate("Short intro", settings.ShortIntroTemplate)...)
	errors = append(errors, validateMetadataTemplate("Compilation intro", settings.CompilationIntroTemplate)...)
	if settings.HashtagMode == "custom" {
		seen := map[string]bool{}
		tags := []string{}
		for _, raw := range settings.CustomHashtags {
			tag := strings.TrimLeft(strings.TrimSpace(raw), "#")
			if !validHashtag(tag) {
				errors = append(errors, fmt.Sprintf("Invalid hashtag %q: use letters, numbers or underscores without spaces", raw))
				continue
			}
			if !seen[strings.ToLower(tag)] {
				tags = append(tags, tag)
				seen[strings.ToLower(tag)] = true
			}
		}
		if len(seen) > MaxMetadataHashtags {
			errors = append(errors, fmt.Sprintf("Custom hashtags exceed %d unique tags; automatic format and category tags also count toward this limit", MaxMetadataHashtags))
		}
		if chars := publicationmetadata.TagCharacters(tags); chars > 500 {
			errors = append(errors, fmt.Sprintf("Custom hashtags use %d tag characters; maximum is 500 including automatic tags", chars))
		}
	}
	return errors
}

func metadataLanguage(filter string, sources []Source) string {
	if filter != "" {
		if filter == "ru" {
			return "ru"
		}
		return "en"
	}
	if len(sources) > 0 {
		for _, source := range sources {
			if source.Language != "ru" || source.LanguageSource != "clip" {
				return "en"
			}
		}
		return "ru"
	}
	return "en"
}

func categoryHashtag(name string) string {
	var result strings.Builder
	for _, ch := range name {
		if unicode.IsLetter(ch) || unicode.IsNumber(ch) || unicode.IsMark(ch) {
			result.WriteRune(ch)
		}
	}
	return result.String()
}

func GenerateMetadata(input MetadataInput) MetadataResult {
	result := MetadataResult{Hashtags: []string{}, Errors: ValidateMetadataSettings(input.Settings)}
	format := input.Format
	if format == "shorts" {
		format = "short"
	}
	if format != "short" && format != "compilation" {
		result.Errors = append(result.Errors, "Format must be short or compilation")
	}
	if len(input.Sources) == 0 {
		result.Errors = append(result.Errors, "Select at least one source clip to build streamer credits")
	} else if format == "short" && len(input.Sources) != 1 {
		result.Errors = append(result.Errors, "A Short must use exactly one source clip")
	}
	settings := MetadataSettings{HashtagMode: "standard", CustomHashtags: []string{}}
	if input.Settings != nil {
		settings = *input.Settings
		settings.CustomHashtags = append([]string{}, input.Settings.CustomHashtags...)
		if settings.HashtagMode == "" {
			settings.HashtagMode = "standard"
		}
	}
	result.Language = metadataLanguage(input.LanguageFilter, input.Sources)
	russian := result.Language == "ru"
	category := ""
	if input.SourceMode == "category" {
		category = strings.TrimSpace(input.CategoryName)
	}
	logins := []string{}
	seen := map[string]bool{}
	sources := make([]MetadataSource, 0, len(input.Sources))
	for _, source := range input.Sources {
		login := strings.ToLower(strings.TrimSpace(source.BroadcasterLogin))
		sources = append(sources, MetadataSource{Login: login, Language: source.Language, LanguageSource: source.LanguageSource})
		if !twitchLogin.MatchString(login) {
			result.Errors = append(result.Errors, "Source clip has no valid Twitch channel login; streamer attribution cannot be generated")
			continue
		}
		if !seen[login] {
			seen[login] = true
			logins = append(logins, login)
		}
	}
	first := ""
	if len(logins) > 0 {
		first = logins[0]
	}
	template := settings.ShortIntroTemplate
	if format == "compilation" {
		template = settings.CompilationIntroTemplate
	}
	if strings.TrimSpace(template) == "" {
		if format == "compilation" {
			template = "{title}"
		} else if russian {
			template = "{title}\nМомент со стрима {streamer}."
		} else {
			template = "{title}\nA moment from {streamer}'s stream."
		}
	}
	values := map[string]string{"title": input.Title, "streamer": first, "streamers": strings.Join(logins, ", "), "category": category, "clip_count": strconv.Itoa(len(input.Sources))}
	result.Intro = strings.TrimSpace(metadataPlaceholder.ReplaceAllStringFunc(template, func(token string) string {
		return values[token[1:len(token)-1]]
	}))
	if input.IntroOverride != nil {
		result.Intro = strings.TrimSpace(*input.IntroOverride)
	}
	blocks := []string{}
	if result.Intro != "" {
		blocks = append(blocks, result.Intro)
	}
	// The actual assembly count is an automatic block, so changing membership
	// keeps it accurate even when an operator wrote a custom introduction.
	if format == "compilation" {
		count := fmt.Sprintf("Twitch highlights compilation: %d clips.", len(input.Sources))
		if russian {
			count = fmt.Sprintf("Подборка моментов Twitch: %d клипов.", len(input.Sources))
		}
		blocks = append(blocks, count)
	}
	if category != "" {
		label := "Twitch category: "
		if russian {
			label = "Категория Twitch: "
		}
		blocks = append(blocks, label+category)
	}
	if len(logins) > 0 {
		label := "Streamers:"
		if russian {
			label = "Стримеры:"
		}
		lines := []string{label}
		for _, login := range logins {
			lines = append(lines, login+" — https://www.twitch.tv/"+login)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	contact := "Contact: " + MetadataContactEmail
	if russian {
		contact = "Для связи: " + MetadataContactEmail
	}
	blocks = append(blocks, contact)
	appendTag := func(raw string) {
		tag := strings.TrimLeft(strings.TrimSpace(raw), "#")
		if !validHashtag(tag) {
			return
		}
		for _, existing := range result.Hashtags {
			if strings.EqualFold(existing, "#"+tag) {
				return
			}
		}
		result.Hashtags = append(result.Hashtags, "#"+tag)
	}
	if settings.HashtagMode == "custom" {
		for _, tag := range settings.CustomHashtags {
			appendTag(tag)
		}
	} else {
		for _, tag := range []string{"twitch", "clips", "highlights"} {
			appendTag(tag)
		}
		if russian {
			for _, tag := range []string{"твич", "моменты", "приколы"} {
				appendTag(tag)
			}
		}
	}
	if format == "short" {
		appendTag("shorts")
		appendTag(first)
	}
	if category != "" {
		appendTag(categoryHashtag(category))
	}
	if len(result.Hashtags) > MaxMetadataHashtags {
		result.Errors = append(result.Errors, fmt.Sprintf("Final hashtags total %d; maximum is %d including automatic tags", len(result.Hashtags), MaxMetadataHashtags))
	}
	prepared := publicationmetadata.Compose(strings.Join(blocks, "\n\n"), result.Hashtags)
	result.Description = prepared.Description
	result.DescriptionBytes = len([]byte(result.Description))
	result.TagCharacters = publicationmetadata.TagCharacters(prepared.Tags)
	result.Errors = append(result.Errors, publicationmetadata.ValidationErrors(input.Title, prepared)...)
	result.Snapshot = MetadataSnapshot{SchemaVersion: MetadataSchemaVersion, Format: format, Settings: settings,
		SourceMode: input.SourceMode, CategoryName: category, LanguageFilter: input.LanguageFilter, Language: result.Language,
		Sources: sources, Intro: result.Intro, IntroEdited: input.IntroOverride != nil}
	return result
}
