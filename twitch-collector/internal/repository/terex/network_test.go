package terex

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"terex/twitch-collector/internal/domain"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestRequestFailuresExplainCauseWithoutLeakingURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"deadline", context.DeadlineExceeded, "terex_timeout"},
		{"dns", &net.DNSError{Err: "lookup failed", Name: "secret-host"}, "terex_dns_unavailable"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "terex_connection_refused"},
		{"closed", syscall.ECONNRESET, "terex_connection_closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New("http://secret-user:secret-password@secret-host", "secret-token")
			c.HTTP.Transport = failingTransport{&url.Error{Op: "Get", URL: "http://secret-host?signature=secret-signature", Err: tc.err}}
			_, err := c.CollectorConfig(context.Background())
			var failure *domain.Failure
			if !errors.As(err, &failure) || failure.Code != tc.code || failure.Operation != "GET /api/twitch/collector-config" || !failure.Retryable {
				t.Fatalf("wrong diagnostic: %+v", err)
			}
			if strings.Contains(failure.Message, "secret") || strings.Contains(failure.Message, "://") {
				t.Fatalf("request diagnostic leaked URL: %s", failure.Message)
			}
		})
	}
}
