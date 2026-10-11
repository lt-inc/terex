package twitch

import (
	"reflect"
	"strings"
	"testing"

	"terex/proto/publicationmetadata"
)

func TestMetadataLanguageDefaultsCustomAndPublisherComposition(t *testing.T) {
	ru := Source{BroadcasterLogin: "Streamer", Language: "ru", LanguageSource: "clip", URL: "https://clips.twitch.tv/internal-source"}
	for _, tc := range []struct {
		name, filter, language string
		sources                []Source
		settings               *MetadataSettings
		tags                   []string
	}{
		{"ru filter", "ru", "ru", []Source{ru}, nil, []string{"#twitch", "#clips", "#highlights", "#твич", "#моменты", "#приколы", "#shorts", "#streamer", "#JustChatting"}},
		{"confirmed ru", "", "ru", []Source{ru}, nil, []string{"#twitch", "#clips", "#highlights", "#твич", "#моменты", "#приколы", "#shorts", "#streamer", "#JustChatting"}},
		{"channel hint is not confirmed", "", "en", []Source{{BroadcasterLogin: "Streamer", Language: "ru", LanguageSource: "channel"}}, nil, []string{"#twitch", "#clips", "#highlights", "#shorts", "#streamer", "#JustChatting"}},
		{"other language filter", "en", "en", []Source{ru}, nil, []string{"#twitch", "#clips", "#highlights", "#shorts", "#streamer", "#JustChatting"}},
		{"custom replaces language defaults", "ru", "ru", []Source{ru}, &MetadataSettings{HashtagMode: "custom", CustomHashtags: []string{"#Funny", "funny", "#SHORTS"}}, []string{"#Funny", "#SHORTS", "#streamer", "#JustChatting"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := GenerateMetadata(MetadataInput{Format: "short", Title: "Funny moment", SourceMode: "category", CategoryName: "Just Chatting!", LanguageFilter: tc.filter, Sources: tc.sources, Settings: tc.settings})
			if len(result.Errors) != 0 || result.Language != tc.language || !reflect.DeepEqual(result.Hashtags, tc.tags) {
				t.Fatalf("unexpected metadata: %+v", result)
			}
			if !strings.Contains(result.Description, MetadataContactEmail) || !strings.Contains(result.Description, "https://www.twitch.tv/streamer") || strings.Contains(result.Description, "clips.twitch.tv") {
				t.Fatalf("incorrect public credits: %s", result.Description)
			}
			published := publicationmetadata.Compose(result.Description, result.Hashtags)
			if published.Description != result.Description || strings.Contains(strings.Join(published.Tags, ","), "#") {
				t.Fatalf("publisher differs from preview: %+v", published)
			}
		})
	}
}

func TestCompilationSnapshotPreservesIntroAndOrderedCredits(t *testing.T) {
	sources := []Source{{BroadcasterLogin: "one", Language: "ru", LanguageSource: "clip"}, {BroadcasterLogin: "two", Language: "en", LanguageSource: "clip"}, {BroadcasterLogin: "one", Language: "ru", LanguageSource: "clip"}}
	intro := "My own introduction"
	result := GenerateMetadata(MetadataInput{Format: "compilation", Title: "Compilation", Sources: sources, IntroOverride: &intro})
	if len(result.Errors) != 0 || result.Language != "en" || strings.Contains(strings.Join(result.Hashtags, " "), "#one") || strings.Count(result.Description, "https://www.twitch.tv/one") != 1 {
		t.Fatalf("incorrect compilation: %+v", result)
	}
	updated := GenerateMetadata(result.Snapshot.ToInput("New title", []Source{sources[1], sources[0]}))
	if len(updated.Errors) != 0 || updated.Intro != intro || !updated.Snapshot.IntroEdited || !strings.Contains(updated.Description, "2 clips") || strings.Index(updated.Description, "twitch.tv/two") > strings.Index(updated.Description, "twitch.tv/one") {
		t.Fatalf("composition edit lost introduction or order: %+v", updated)
	}
	defaults := GenerateMetadata(MetadataInput{Format: "compilation", Title: "Compilation", Sources: sources})
	updatedDefaults := GenerateMetadata(defaults.Snapshot.ToInput("New title", []Source{sources[0], sources[1]}))
	if !strings.Contains(updatedDefaults.Description, "2 clips") || !strings.Contains(updatedDefaults.Intro, "New title") {
		t.Fatalf("default introduction did not reflect assembly: %+v", updatedDefaults)
	}
}

func TestMetadataRejectsInvalidTemplatesAndPublicationLimits(t *testing.T) {
	for _, template := range []string{"{unknown}", "{title", "{{title}}", strings.Repeat("я", 2501)} {
		if errors := ValidateMetadataSettings(&MetadataSettings{ShortIntroTemplate: template}); len(errors) == 0 {
			t.Fatalf("invalid template accepted: %q", template)
		}
	}
	for _, raw := range []string{"two words", "tag!"} {
		if errors := ValidateMetadataSettings(&MetadataSettings{HashtagMode: "custom", CustomHashtags: []string{raw}}); len(errors) == 0 {
			t.Fatalf("invalid hashtag accepted: %q", raw)
		}
	}
	source := Source{BroadcasterLogin: "one", Language: "ru", LanguageSource: "clip"}
	intro := strings.Repeat("я", 2500)
	result := GenerateMetadata(MetadataInput{Format: "short", Title: "Title", Sources: []Source{source}, IntroOverride: &intro})
	if len(result.Errors) == 0 || result.DescriptionBytes <= 5000 || !strings.Contains(result.Description, MetadataContactEmail) {
		t.Fatalf("oversized description was truncated or accepted: %+v", result)
	}
	tags := []string{}
	for i := range 19 {
		tags = append(tags, "tag"+string(rune('a'+i)))
	}
	result = GenerateMetadata(MetadataInput{Format: "short", Title: "Title", Sources: []Source{source}, Settings: &MetadataSettings{HashtagMode: "custom", CustomHashtags: tags}})
	if len(result.Errors) == 0 || len(result.Hashtags) != 21 {
		t.Fatalf("final automatic tag limit was ignored or silently truncated: %+v", result)
	}
	result = GenerateMetadata(MetadataInput{Format: "short", Title: "Title", Sources: []Source{source}, Settings: &MetadataSettings{HashtagMode: "custom", CustomHashtags: []string{strings.Repeat("a", 501)}}})
	if len(result.Errors) == 0 || result.TagCharacters <= 500 {
		t.Fatalf("tag characters were not checked: %+v", result)
	}
	prepared := publicationmetadata.Compose("Text #Existing", []string{"#Existing", "clip", "clip", "two words"})
	if prepared.Description != "Text #Existing\n\n#clip #twowords" || strings.Join(prepared.Tags, ",") != "Existing,clip,two words" || publicationmetadata.TagCharacters(prepared.Tags) != 25 {
		t.Fatalf("legacy tag formatting/count changed: %+v", prepared)
	}
}
