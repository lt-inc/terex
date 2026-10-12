package domain

import "fmt"

// FeedHTTPError keeps an upstream status distinct from network and parse errors.
type FeedHTTPError struct {
	StatusCode int
	URL        string
}

func (e *FeedHTTPError) Error() string {
	return fmt.Sprintf("YouTube feed GET %s returned HTTP %d", e.URL, e.StatusCode)
}
