package service

import (
	"math"
	"sort"
	"strings"
	"time"

	"terex/twitch-collector/internal/domain"
)

// Select gives each channel a first choice before selecting a second choice.
// Reservation limits are also enforced atomically by the repository.
func Select(clips []domain.Clip, settings domain.Settings, now time.Time) []domain.Clip {
	s := settings
	eligible := make([]domain.Clip, 0, len(clips))
	seen := map[string]bool{}
	for _, c := range clips {
		age := now.Sub(c.CreatedAt)
		if c.ID == "" || c.ChannelID == "" || seen[c.ID] || c.ViewCount < s.MinViews || age < time.Duration(s.MinAgeMinutes)*time.Minute || age > time.Duration(s.WindowHours)*time.Hour || math.IsNaN(c.Duration) || math.IsInf(c.Duration, 0) || c.Duration < s.MinDurationSec || c.Duration > s.MaxDurationSec {
			continue
		}
		if c.State != "" && c.State != "discovered" {
			continue
		}
		seen[c.ID] = true
		eligible = append(eligible, c)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].ViewCount != eligible[j].ViewCount {
			return eligible[i].ViewCount > eligible[j].ViewCount
		}
		if !eligible[i].CreatedAt.Equal(eligible[j].CreatedAt) {
			return eligible[i].CreatedAt.After(eligible[j].CreatedAt)
		}
		return eligible[i].ID < eligible[j].ID
	})
	unique := make([]domain.Clip, 0, len(eligible))
	for _, c := range eligible {
		duplicate := false
		for _, chosen := range unique {
			if Overlaps(c, chosen) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, c)
		}
	}
	result := make([]domain.Clip, 0, len(unique))
	counts := map[string]int{}
	// Return all eligible choices: the transaction skips already reserved IDs
	// and applies today's remaining quotas before choosing the next candidate.
	for round := 0; round < len(unique); round++ {
		for _, c := range unique {
			if counts[c.ChannelID] != round {
				continue
			}
			already := false
			for _, selected := range result {
				if c.ID == selected.ID {
					already = true
					break
				}
			}
			if already {
				continue
			}
			result = append(result, c)
			counts[c.ChannelID]++
		}
	}
	return result
}

func Overlaps(a, b domain.Clip) bool {
	if strings.TrimSpace(a.VideoID) == "" || a.VideoID != b.VideoID || a.VODOffset == nil || b.VODOffset == nil {
		return false
	}
	intersection := math.Min(*a.VODOffset+a.Duration, *b.VODOffset+b.Duration) - math.Max(*a.VODOffset, *b.VODOffset)
	return intersection > 0
}

func RetryAt(attempt int, failure *domain.Failure, now time.Time) *time.Time {
	if failure == nil || !failure.Retryable || attempt >= 3 {
		return nil
	}
	delay := 5 * time.Minute
	if attempt >= 2 {
		delay = 30 * time.Minute
	}
	if failure.RetryAfter > delay {
		delay = failure.RetryAfter
	}
	when := now.Add(delay)
	return &when
}
