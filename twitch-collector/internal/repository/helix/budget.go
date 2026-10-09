package helix

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"sync"
	"terex/twitch-collector/internal/domain"
	"time"
)

type discoveryBudget struct {
	mu          sync.Mutex
	limit, used int
	parent      *discoveryBudget
}
type budgetKey struct{}

func discoveryEnv(name string, fallback, minimum int) int {
	n, e := strconv.Atoi(os.Getenv(name))
	if e != nil || n < minimum {
		return fallback
	}
	return n
}
func (c *Client) BeginDiscoveryCycle(ctx context.Context) context.Context {
	n := c.DiscoveryMaxRequests
	if n < 1 {
		n = 120
	}
	return context.WithValue(ctx, budgetKey{}, &discoveryBudget{limit: n})
}
func (c *Client) DiscoveryPhase(ctx context.Context, limit int) context.Context {
	p, _ := ctx.Value(budgetKey{}).(*discoveryBudget)
	return context.WithValue(ctx, budgetKey{}, &discoveryBudget{limit: limit, parent: p})
}
func (c *Client) DiscoveryRemaining(ctx context.Context) int {
	b, _ := ctx.Value(budgetKey{}).(*discoveryBudget)
	if b == nil {
		return 120
	}
	b.mu.Lock()
	n := b.limit - b.used
	b.mu.Unlock()
	if b.parent != nil {
		b.parent.mu.Lock()
		n = min(n, b.parent.limit-b.parent.used)
		b.parent.mu.Unlock()
	}
	return max(0, n)
}
func (c *Client) DiscoveryUsed(ctx context.Context) int {
	b, _ := ctx.Value(budgetKey{}).(*discoveryBudget)
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}
func chargeBudget(ctx context.Context) error {
	b, _ := ctx.Value(budgetKey{}).(*discoveryBudget)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.parent != nil {
		b.parent.mu.Lock()
		defer b.parent.mu.Unlock()
		if b.parent.used >= b.parent.limit {
			return budgetFailure()
		}
	}
	if b.used >= b.limit {
		return budgetFailure()
	}
	b.used++
	if b.parent != nil {
		b.parent.used++
	}
	return nil
}
func budgetFailure() error {
	return &domain.Failure{Kind: "twitch_metadata", Code: "discovery_budget", Message: "Поиск продолжится при следующей проверке: исчерпан бюджет запросов Twitch"}
}
func (c *Client) beforeMetadata(ctx context.Context) error {
	for {
		c.limitMu.Lock()
		now := time.Now()
		if c.rateKnown && now.Before(c.rateReset) && c.rateRemaining <= c.DiscoveryRateFloor {
			wait := time.Until(c.rateReset)
			c.limitMu.Unlock()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		rps := c.DiscoveryRPS
		if rps < 1 {
			rps = 5
		}
		at := c.nextRequest
		if at.Before(now) {
			at = now
		}
		c.nextRequest = at.Add(time.Second / time.Duration(rps))
		c.limitMu.Unlock()
		if delay := time.Until(at); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		return chargeBudget(ctx)
	}
}
func (c *Client) observeRate(h http.Header) {
	remaining, err := strconv.Atoi(h.Get("Ratelimit-Remaining"))
	reset, e := strconv.ParseInt(h.Get("Ratelimit-Reset"), 10, 64)
	if err != nil || e != nil {
		return
	}
	c.limitMu.Lock()
	defer c.limitMu.Unlock()
	c.rateKnown = true
	c.rateRemaining = remaining
	c.rateReset = time.Unix(reset, 0)
}
func (c *Client) rememberRateLimit(h http.Header) {
	c.limitMu.Lock()
	defer c.limitMu.Unlock()
	if !c.rateReset.After(time.Now()) {
		delay := time.Minute
		if seconds, e := strconv.Atoi(h.Get("Retry-After")); e == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
		c.rateReset = time.Now().Add(delay)
	}
	c.rateKnown = true
	c.rateRemaining = 0
}
func (c *Client) rateFailure() error {
	c.limitMu.Lock()
	at := c.rateReset
	c.limitMu.Unlock()
	retry := time.Until(at)
	if retry < time.Second {
		retry = time.Minute
	}
	return &domain.Failure{Kind: "twitch_metadata", Code: "twitch_rate_limit", Message: "Поиск отложен до восстановления лимита Twitch API", Retryable: true, RetryAfter: retry, HTTPStatus: http.StatusTooManyRequests}
}
