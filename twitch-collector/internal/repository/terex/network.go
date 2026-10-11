package terex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"terex/twitch-collector/internal/domain"
)

// Never print the original HTTP error: it can contain a signed upload URL or
// credentials in a configured base URL. Keep an actionable, fixed diagnostic.
func requestFailure(ctx context.Context, err error, operation string, elapsed time.Duration) *domain.Failure {
	code, reason := "terex_network_unavailable", "сетевой запрос к Terex не выполнен"
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		code, reason = "request_canceled", "запрос отменён"
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		code, reason = "terex_timeout", "Terex не ответил до истечения времени ожидания"
	case errors.As(err, &dns):
		code, reason = "terex_dns_unavailable", "не удалось определить сетевой адрес Terex"
	case errors.As(err, &network) && network.Timeout():
		code, reason = "terex_timeout", "истекло время ожидания сетевого ответа Terex"
	case errors.Is(err, syscall.ECONNREFUSED):
		code, reason = "terex_connection_refused", "Terex отклонил соединение; проверьте доступность backend"
	case errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		code, reason = "terex_connection_closed", "соединение с Terex прервано до завершения ответа"
	}
	return &domain.Failure{Kind: "network", Code: code, Message: fmt.Sprintf("%s (%s, %d мс)", reason, operation, elapsed.Milliseconds()), Operation: operation, DurationMS: elapsed.Milliseconds(), Retryable: true}
}

func isNetworkError(err error) bool {
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
