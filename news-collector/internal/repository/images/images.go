// Package images finds and validates pictures for posts: Pexels stock photos by search query,
// and a check that a URL really is a reasonably sized image Telegram can fetch.
package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxBytes is Telegram's limit for a photo sent by URL.
const MaxBytes = 5 << 20

var ErrNotConfigured = errors.New("stock photo API key is not configured")

type Photo struct {
	URL         string `json:"url"`        // image to send
	Thumb       string `json:"thumb"`      // small preview for the panel
	Credit      string `json:"credit"`     // photographer
	CreditURL   string `json:"credit_url"` // photo page on Pexels
	Source      string `json:"source"`     // pexels | article
	Description string `json:"description"`
}

// Searcher is a stock-photo provider.
type Searcher interface {
	Name() string // pexels | pixabay
	Ready() bool
	Search(ctx context.Context, query, lang string, limit int) ([]Photo, error)
}

type Pexels struct {
	Base string // default https://api.pexels.com
	Key  string
	HTTP *http.Client
}

func NewPexels(key, base string) *Pexels {
	if base == "" {
		base = "https://api.pexels.com"
	}
	return &Pexels{Base: strings.TrimRight(base, "/"), Key: key, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (p *Pexels) Name() string { return "pexels" }

func (p *Pexels) Ready() bool { return p != nil && p.Key != "" }

// Search returns landscape stock photos for the query. lang picks the result locale.
func (p *Pexels) Search(ctx context.Context, query, lang string, limit int) ([]Photo, error) {
	if !p.Ready() {
		return nil, ErrNotConfigured
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty image query")
	}
	locale := "ru-RU"
	if lang == "en" {
		locale = "en-US"
	}
	v := url.Values{"query": {query}, "per_page": {strconv.Itoa(limit)}, "orientation": {"landscape"}, "locale": {locale}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Base+"/v1/search?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", p.Key)
	res, err := p.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("pexels unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pexels returned HTTP %d", res.StatusCode)
	}
	var out struct {
		Photos []struct {
			URL          string `json:"url"`
			Photographer string `json:"photographer"`
			Alt          string `json:"alt"`
			Src          struct {
				Large  string `json:"large"`
				Medium string `json:"medium"`
			} `json:"src"`
		} `json:"photos"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, errors.New("pexels returned invalid JSON")
	}
	photos := make([]Photo, 0, len(out.Photos))
	for _, ph := range out.Photos {
		if ph.Src.Large == "" {
			continue
		}
		thumb := ph.Src.Medium
		if thumb == "" {
			thumb = ph.Src.Large
		}
		photos = append(photos, Photo{URL: ph.Src.Large, Thumb: thumb, Credit: ph.Photographer, CreditURL: ph.URL, Source: "pexels", Description: ph.Alt})
	}
	return photos, nil
}

// Checker verifies that a URL serves an image Telegram will accept.
type Checker struct {
	HTTP *http.Client
}

var okTypes = map[string]bool{"image/jpeg": true, "image/jpg": true, "image/png": true, "image/webp": true, "image/gif": true}

// Check fetches only the headers (HEAD, then GET without reading the body when HEAD is refused).
func (c *Checker) Check(ctx context.Context, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(raw) > 2000 {
		return errors.New("not a valid image URL")
	}
	do := func(method string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TerexNewsCollector/1.0)")
		req.Header.Set("Accept", "image/*")
		return c.HTTP.Do(req)
	}
	res, err := do(http.MethodHead)
	if err == nil && (res.StatusCode == http.StatusMethodNotAllowed || res.StatusCode == http.StatusNotImplemented || res.StatusCode == http.StatusForbidden) {
		res.Body.Close()
		res, err = do(http.MethodGet)
	}
	if err != nil {
		return errors.New("image is unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("image returned HTTP %d", res.StatusCode)
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(res.Header.Get("Content-Type"), ";", 2)[0]))
	if !okTypes[ct] {
		return fmt.Errorf("not an image (%s)", ct)
	}
	if res.ContentLength > MaxBytes {
		return errors.New("image is larger than 5 MB")
	}
	return nil
}

// Pixabay searches pixabay.com. Its license does not require attribution, so no credit is shown.
type Pixabay struct {
	Base string // default https://pixabay.com
	Key  string
	HTTP *http.Client
}

func NewPixabay(key, base string) *Pixabay {
	if base == "" {
		base = "https://pixabay.com"
	}
	return &Pixabay{Base: strings.TrimRight(base, "/"), Key: key, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (p *Pixabay) Name() string { return "pixabay" }

func (p *Pixabay) Ready() bool { return p != nil && p.Key != "" }

func (p *Pixabay) Search(ctx context.Context, query, lang string, limit int) ([]Photo, error) {
	if !p.Ready() {
		return nil, ErrNotConfigured
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty image query")
	}
	if r := []rune(query); len(r) > 100 { // Pixabay's limit
		query = string(r[:100])
	}
	l := "ru"
	if lang == "en" {
		l = "en"
	}
	v := url.Values{"key": {p.Key}, "q": {query}, "lang": {l}, "image_type": {"photo"}, "orientation": {"horizontal"},
		"safesearch": {"true"}, "order": {"popular"}, "per_page": {strconv.Itoa(max(limit, 3))}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Base+"/api/?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := p.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("pixabay unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pixabay returned HTTP %d", res.StatusCode)
	}
	var out struct {
		Hits []struct {
			PageURL       string `json:"pageURL"`
			Tags          string `json:"tags"`
			PreviewURL    string `json:"previewURL"`
			WebformatURL  string `json:"webformatURL"`
			LargeImageURL string `json:"largeImageURL"`
		} `json:"hits"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, errors.New("pixabay returned invalid JSON")
	}
	photos := make([]Photo, 0, len(out.Hits))
	for _, h := range out.Hits {
		img := h.LargeImageURL
		if img == "" {
			img = h.WebformatURL
		}
		if img == "" {
			continue
		}
		thumb := h.WebformatURL
		if thumb == "" {
			thumb = h.PreviewURL
		}
		photos = append(photos, Photo{URL: img, Thumb: thumb, CreditURL: h.PageURL, Source: "pixabay", Description: h.Tags})
	}
	if len(photos) > limit {
		photos = photos[:limit]
	}
	return photos, nil
}

// Download fetches an image Telegram can be given as an upload (so the host's hotlink
// protection or bot blocking cannot stop it). The size limit is enforced while reading.
func (c *Checker) Download(ctx context.Context, raw string) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, "", errors.New("not a valid image URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TerexNewsCollector/1.0)")
	req.Header.Set("Accept", "image/*")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", errors.New("image is unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxBytes+1))
	if err != nil {
		return nil, "", errors.New("image download failed")
	}
	if len(data) > MaxBytes {
		return nil, "", errors.New("image is larger than 5 MB")
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(res.Header.Get("Content-Type"), ";", 2)[0]))
	if !okTypes[ct] {
		ct = strings.ToLower(http.DetectContentType(data)) // some CDNs label images application/octet-stream
		if !okTypes[ct] {
			return nil, "", fmt.Errorf("not an image (%s)", ct)
		}
	}
	return data, ct, nil
}
