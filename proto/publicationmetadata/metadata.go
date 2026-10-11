// Package publicationmetadata owns the description format and YouTube limits
// used by both the editor preview and the publishing adapter.
package publicationmetadata

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type Prepared struct {
	Description string
	Tags        []string
}

// Compose preserves legacy tag spelling for snippet.tags, while appending any
// missing visible hashtags exactly once. Generated descriptions are idempotent.
func Compose(description string, hashtags []string) Prepared {
	seen := map[string]bool{}
	for _, word := range strings.Fields(description) {
		seen[strings.ToLower(word)] = true
	}
	tags := []string{}
	visible := []string{}
	tagSeen := map[string]bool{}
	for _, raw := range hashtags {
		tag := strings.TrimLeft(strings.TrimSpace(raw), "#")
		key := strings.ToLower(tag)
		if tag == "" || tagSeen[key] {
			continue
		}
		tagSeen[key] = true
		tags = append(tags, tag)
		hashtag := "#" + strings.Join(strings.Fields(tag), "")
		if !seen[strings.ToLower(hashtag)] {
			visible = append(visible, hashtag)
			seen[strings.ToLower(hashtag)] = true
		}
	}
	if len(visible) > 0 {
		if description != "" {
			description += "\n\n"
		}
		description += strings.Join(visible, " ")
	}
	return Prepared{Description: description, Tags: tags}
}

// TagCharacters includes YouTube's comma separators and quotation overhead for
// tags with spaces. Description limits are bytes, rather than Unicode runes.
func TagCharacters(tags []string) int {
	chars := 0
	for i, tag := range tags {
		chars += utf8.RuneCountInString(tag)
		if i > 0 {
			chars++
		}
		if strings.ContainsAny(tag, " \t") {
			chars += 2
		}
	}
	return chars
}

func ValidationErrors(title string, prepared Prepared) []string {
	errors := []string{}
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 100 {
		errors = append(errors, "YouTube title must contain 1–100 characters")
	}
	if len([]byte(prepared.Description)) > 5000 {
		errors = append(errors, fmt.Sprintf("YouTube description uses %d bytes; maximum is 5000 bytes", len([]byte(prepared.Description))))
	}
	if strings.ContainsAny(title, "<>") || strings.ContainsAny(prepared.Description, "<>") {
		errors = append(errors, "YouTube title and description cannot contain angle brackets")
	}
	if chars := TagCharacters(prepared.Tags); chars > 500 {
		errors = append(errors, fmt.Sprintf("YouTube tags use %d characters; maximum is 500 characters", chars))
	}
	return errors
}

func Validate(title string, prepared Prepared) error {
	if errors := ValidationErrors(title, prepared); len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return nil
}
