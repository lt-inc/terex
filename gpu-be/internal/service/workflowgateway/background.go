package workflowgateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/mediatmp"
	"terex/proto/workflow"
)

// BackgroundDownloader fetches a YouTube video into dir and returns the file path.
type BackgroundDownloader interface {
	DownloadBackground(ctx context.Context, url, dir string) (string, error)
}

var youtubeHosts = map[string]bool{"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true, "youtu.be": true}

// youtubeURL accepts only YouTube links: the address goes to yt-dlp, which would fetch anything.
func youtubeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || !youtubeHosts[strings.ToLower(u.Hostname())] {
		return "", fmt.Errorf("expected a YouTube video link")
	}
	return u.String(), nil
}

// objectSlug keeps the name readable in the bucket without anything a path could misuse.
func objectSlug(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		}
		if b.Len() >= 60 {
			break
		}
	}
	if b.Len() == 0 {
		return "youtube"
	}
	return b.String()
}

// ImportBackground downloads a YouTube video into the backgrounds bucket; main-be registers the record.
func (s *Service) ImportBackground(ctx context.Context, q *workflow.ImportBackgroundRequest) (*workflow.ImportBackgroundResponse, error) {
	if s.Backgrounds == nil {
		return nil, fmt.Errorf("background import is not configured")
	}
	link, err := youtubeURL(q.URL)
	if err != nil {
		return nil, err
	}
	ctx, release, err := mediatmp.Begin(ctx, s.Resources)
	if err != nil {
		return nil, err
	}
	defer release()
	dir, cleanup, err := mediatmp.MkdirTemp(ctx, s.Resources, s.TmpDir, "background-")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	file, err := s.Backgrounds.DownloadBackground(ctx, link, dir)
	if err != nil {
		return nil, err
	}
	rnd := make([]byte, 6)
	_, _ = rand.Read(rnd)
	ref := domain.ObjectRef{Bucket: domain.BucketBackgrounds, Object: hex.EncodeToString(rnd) + "_" + objectSlug(q.Name) + filepath.Ext(file)}
	if err = s.Store.Upload(ctx, ref, file); err != nil {
		return nil, err
	}
	return &workflow.ImportBackgroundResponse{Bucket: ref.Bucket, Object: ref.Object}, nil
}
