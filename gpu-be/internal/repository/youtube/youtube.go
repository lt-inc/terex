// Package youtube implements the official Data API. All publishing is performed
// with owner OAuth; no browser automation or service-account impersonation is used.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/proto/publicationmetadata"
)

type TokenSource func(context.Context) (string, error)

type Client struct {
	HTTP      *http.Client
	APIURL    string
	UploadURL string
	Token     TokenSource
	ChunkSize int64
}

func New(token TokenSource) *Client {
	return &Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, APIURL: "https://www.googleapis.com/youtube/v3", UploadURL: "https://www.googleapis.com/upload/youtube/v3/videos", Token: token, ChunkSize: 8 * 1024 * 1024}
}

type APIError struct {
	Status     int
	Reason     string
	RetryAfter time.Duration
}

type transportError struct{ cause error }

func (e *transportError) Error() string { return "YouTube HTTP transport failed" }
func (e *transportError) Unwrap() error { return e.cause }

func (e *APIError) Error() string   { return fmt.Sprintf("YouTube API HTTP %d: %s", e.Status, e.Reason) }
func (e *APIError) Retryable() bool { return e.Status == 429 || e.Status >= 500 }

func (c *Client) Capabilities() domain.PublishingCapabilities {
	return domain.PublishingCapabilities{ResumableUpload: true, DeferredVisibility: true}
}

func (c *Client) Validate(m domain.PublicationMetadata) error {
	m = youtubeMetadata(m)
	return publicationmetadata.Validate(m.Title, publicationmetadata.Prepared{Description: m.Description, Tags: m.Tags})
}

// Terex's tags are hashtags. YouTube displays hashtags in the description;
// snippet.tags is also populated for search metadata, without the leading #.
func youtubeMetadata(m domain.PublicationMetadata) domain.PublicationMetadata {
	prepared := publicationmetadata.Compose(m.Description, m.Tags)
	m.Description, m.Tags = prepared.Description, prepared.Tags
	return m
}

func (c *Client) request(ctx context.Context, method, target string, body io.Reader, headers map[string]string) (*http.Response, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, &transportError{cause: err}
	}
	r.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		if k == "Content-Length" {
			r.ContentLength, err = strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, err
			}
		} else {
			r.Header.Set(k, v)
		}
	}
	// A resumable HTTP 308 is progress, not a redirect. Never follow Location and
	// accidentally replay a PUT or forward OAuth to another host.
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return nil, &transportError{cause: err}
	}
	return response, nil
}

func apiError(r *http.Response) error {
	defer r.Body.Close()
	var wire struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&wire)
	reason := "request failed"
	if len(wire.Error.Errors) > 0 {
		reason = wire.Error.Errors[0].Reason
	}
	// Do not echo arbitrary response bodies: they can contain a credential or session URL.
	retry := time.Duration(0)
	if n, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil {
		retry = time.Duration(n) * time.Second
	} else if at, err := http.ParseTime(r.Header.Get("Retry-After")); err == nil {
		retry = time.Until(at)
	}
	return &APIError{Status: r.StatusCode, Reason: reason, RetryAfter: retry}
}

func (c *Client) Prepare(ctx context.Context, m domain.PublicationMetadata, size int64) (domain.UploadSession, error) {
	if size <= 0 || strings.TrimSpace(m.Title) == "" {
		return domain.UploadSession{}, fmt.Errorf("video size and title are required")
	}
	if err := c.Validate(m); err != nil {
		return domain.UploadSession{}, err
	}
	m = youtubeMetadata(m)
	if m.CategoryID == "" {
		m.CategoryID = "22"
	}
	body, _ := json.Marshal(map[string]any{"snippet": map[string]any{"title": m.Title, "description": m.Description, "tags": m.Tags, "categoryId": m.CategoryID}, "status": map[string]any{"privacyStatus": "private", "selfDeclaredMadeForKids": m.MadeForKids, "containsSyntheticMedia": m.ContainsSyntheticMedia}})
	r, err := c.request(ctx, http.MethodPost, c.UploadURL+"?uploadType=resumable&part=snippet,status", bytes.NewReader(body), map[string]string{"Content-Type": "application/json; charset=UTF-8", "X-Upload-Content-Length": strconv.FormatInt(size, 10), "X-Upload-Content-Type": "video/mp4"})
	if err != nil {
		return domain.UploadSession{}, err
	}
	if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusCreated {
		return domain.UploadSession{}, apiError(r)
	}
	defer r.Body.Close()
	session := r.Header.Get("Location")
	if session == "" {
		return domain.UploadSession{}, fmt.Errorf("YouTube did not return an upload session")
	}
	if err = c.validateSession(session); err != nil {
		return domain.UploadSession{}, err
	}
	return domain.UploadSession{URI: session, Size: size}, nil
}

func parseProgress(r *http.Response, s domain.UploadSession) (domain.UploadSession, error) {
	defer r.Body.Close()
	if r.StatusCode == 308 {
		s.Offset = 0
		if v := r.Header.Get("Range"); v != "" {
			if !strings.HasPrefix(v, "bytes=0-") {
				return s, fmt.Errorf("invalid upload range")
			}
			last, err := strconv.ParseInt(strings.TrimPrefix(v, "bytes=0-"), 10, 64)
			if err != nil || last < 0 || last >= s.Size {
				return s, fmt.Errorf("invalid upload offset")
			}
			s.Offset = last + 1
		}
		return s, nil
	}
	if r.StatusCode == 200 || r.StatusCode == 201 {
		var v struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&v); err != nil {
			return s, err
		}
		if v.ID == "" {
			return s, fmt.Errorf("completed upload missing video id")
		}
		s.VideoID = v.ID
		s.Offset = s.Size
		return s, nil
	}
	// Expired sessions may have completed before their acknowledgement was lost.
	if r.StatusCode == 404 || r.StatusCode == 410 {
		return s, domain.ErrUploadUncertain
	}
	// apiError closes the same body; avoid losing response metadata.
	return s, apiError(r)
}

func (c *Client) Transfer(ctx context.Context, path string, s domain.UploadSession, checkpoint func(domain.UploadSession) error) (domain.UploadSession, error) {
	if s.VideoID != "" {
		return s, nil
	}
	if s.URI == "" || s.Size <= 0 {
		return s, fmt.Errorf("missing resumable session")
	}
	if err := c.validateSession(s.URI); err != nil {
		return s, err
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return s, err
	}
	if st.Size() != s.Size {
		return s, fmt.Errorf("rendered artifact changed during upload")
	}
	// Query even on the first attempt. YouTube, rather than our local offset, is authoritative.
	r, err := c.request(ctx, http.MethodPut, s.URI, nil, map[string]string{"Content-Length": "0", "Content-Range": fmt.Sprintf("bytes */%d", s.Size)})
	if err != nil {
		return s, err
	}
	s, err = parseProgress(r, s)
	if err != nil {
		return s, err
	}
	if err = checkpoint(s); err != nil {
		return s, err
	}
	if s.VideoID != "" {
		return s, nil
	}
	chunk := c.ChunkSize
	if chunk <= 0 {
		chunk = 8 * 1024 * 1024
	}
	if chunk%(256*1024) != 0 {
		return s, fmt.Errorf("chunk size must be a multiple of 256 KiB")
	}
	for s.Offset < s.Size {
		start := s.Offset
		n := min(chunk, s.Size-start)
		if start+n == s.Size {
			s.FinalRequestStarted = true
			if err = checkpoint(s); err != nil {
				return s, err
			}
		}
		r, err = c.request(ctx, http.MethodPut, s.URI, io.NewSectionReader(f, start, n), map[string]string{"Content-Type": "video/mp4", "Content-Length": strconv.FormatInt(n, 10), "Content-Range": fmt.Sprintf("bytes %d-%d/%d", start, start+n-1, s.Size)})
		if err != nil {
			return s, err
		}
		s, err = parseProgress(r, s)
		if err != nil {
			return s, err
		}
		if s.Offset <= start && s.VideoID == "" {
			return s, fmt.Errorf("upload made no progress")
		}
		if err = checkpoint(s); err != nil {
			return s, err
		}
		if s.VideoID != "" {
			return s, nil
		}
	}
	return s, fmt.Errorf("upload consumed all bytes without completion")
}

func (c *Client) validateSession(value string) error {
	u, err := url.Parse(value)
	base, e := url.Parse(c.UploadURL)
	if err != nil || e != nil || u.User != nil || u.Host != base.Host || u.Scheme != base.Scheme {
		return fmt.Errorf("YouTube returned an unexpected upload session origin")
	}
	return nil
}

type videoWire struct {
	ID     string `json:"id"`
	Status struct {
		Privacy                string `json:"privacyStatus"`
		UploadStatus           string `json:"uploadStatus"`
		FailureReason          string `json:"failureReason"`
		RejectionReason        string `json:"rejectionReason"`
		MadeForKids            *bool  `json:"selfDeclaredMadeForKids"`
		ContainsSyntheticMedia *bool  `json:"containsSyntheticMedia"`
		License                string `json:"license"`
		Embeddable             *bool  `json:"embeddable"`
		PublicStatsViewable    *bool  `json:"publicStatsViewable"`
	} `json:"status"`
	Processing struct {
		Status  string `json:"processingStatus"`
		Failure string `json:"processingFailureReason"`
	} `json:"processingDetails"`
}

func (c *Client) readVideo(ctx context.Context, id string) (videoWire, error) {
	r, err := c.request(ctx, http.MethodGet, c.APIURL+"/videos?part=status,processingDetails&id="+url.QueryEscape(id), nil, nil)
	if err != nil {
		return videoWire{}, err
	}
	if r.StatusCode != 200 {
		return videoWire{}, apiError(r)
	}
	defer r.Body.Close()
	var data struct {
		Items []videoWire `json:"items"`
	}
	if err = json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&data); err != nil {
		return videoWire{}, err
	}
	if len(data.Items) != 1 || data.Items[0].ID != id {
		return videoWire{}, fmt.Errorf("video not found for connected owner")
	}
	return data.Items[0], nil
}

func (c *Client) Inspect(ctx context.Context, id string) (domain.RemoteVideo, error) {
	v, err := c.readVideo(ctx, id)
	if err != nil {
		return domain.RemoteVideo{}, err
	}
	return domain.RemoteVideo{ID: v.ID, Privacy: v.Status.Privacy, UploadStatus: v.Status.UploadStatus, ProcessingStatus: v.Processing.Status, Failure: strings.Trim(strings.Join([]string{v.Status.FailureReason, v.Status.RejectionReason, v.Processing.Failure}, " "), " ")}, nil
}

func (c *Client) MakePublic(ctx context.Context, id string) error {
	return c.SetVisibility(ctx, id, "public")
}
func (c *Client) SetVisibility(ctx context.Context, id, visibility string) error {
	if visibility != "public" && visibility != "private" && visibility != "unlisted" {
		return fmt.Errorf("invalid visibility")
	}
	v, err := c.readVideo(ctx, id)
	if err != nil {
		return err
	}
	if v.Status.Privacy == visibility {
		return nil
	}
	if v.Processing.Status != "succeeded" && v.Status.UploadStatus != "processed" {
		return fmt.Errorf("video is not processed")
	}
	// videos.update replaces writable properties in part=status. Preserve those
	// returned by the owner read, and omit publishAt for immediate publication.
	s := map[string]any{"privacyStatus": visibility}
	if v.Status.MadeForKids != nil {
		s["selfDeclaredMadeForKids"] = *v.Status.MadeForKids
	}
	if v.Status.ContainsSyntheticMedia != nil {
		s["containsSyntheticMedia"] = *v.Status.ContainsSyntheticMedia
	}
	if v.Status.License != "" {
		s["license"] = v.Status.License
	}
	if v.Status.Embeddable != nil {
		s["embeddable"] = *v.Status.Embeddable
	}
	if v.Status.PublicStatsViewable != nil {
		s["publicStatsViewable"] = *v.Status.PublicStatsViewable
	}
	body, _ := json.Marshal(map[string]any{"id": id, "status": s})
	r, err := c.request(ctx, http.MethodPut, c.APIURL+"/videos?part=status", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	if r.StatusCode != 200 {
		return apiError(r)
	}
	r.Body.Close()
	confirmed, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}
	if confirmed.Privacy != visibility {
		return &APIError{Status: 403, Reason: "YouTube has not confirmed requested visibility; check API project audit restrictions"}
	}
	return nil
}

func (c *Client) Channel(ctx context.Context) (string, string, error) {
	r, err := c.request(ctx, http.MethodGet, c.APIURL+"/channels?part=id,snippet&mine=true", nil, nil)
	if err != nil {
		return "", "", err
	}
	if r.StatusCode != 200 {
		return "", "", apiError(r)
	}
	defer r.Body.Close()
	var data struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err = json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&data); err != nil {
		return "", "", err
	}
	if len(data.Items) != 1 {
		return "", "", fmt.Errorf("OAuth connection must select exactly one YouTube channel")
	}
	return data.Items[0].ID, data.Items[0].Snippet.Title, nil
}
