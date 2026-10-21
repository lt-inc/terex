// Package assets manages banners, backgrounds and accounts. Files live in gpu-be's MinIO;
// this service only keeps references and asks gpu-be (gRPC) for presigned URLs.
package assets

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"terex/main-be/internal/domain"
	"terex/proto/workflow"
)

type Service struct {
	Assets   domain.AssetRepo
	Accounts domain.AccountRepo
	Gpu      domain.Gpu
	// Importer downloads a YouTube video into the backgrounds bucket (gpu-be, yt-dlp).
	Importer interface {
		ImportBackground(context.Context, *workflow.ImportBackgroundRequest) (*workflow.ImportBackgroundResponse, error)
	}
}

// ── banners / backgrounds ────────────────────────────────────────────────────

func (s *Service) List(ctx context.Context, kind domain.AssetKind) ([]domain.Asset, error) {
	items, err := s.Assets.ListAssets(ctx, kind)
	if err != nil {
		return nil, err
	}
	for i := range items {
		s.fillPreview(ctx, &items[i])
	}
	return items, nil
}

func (s *Service) fillPreview(ctx context.Context, a *domain.Asset) {
	if a.Bucket == nil || a.FileName == nil {
		return
	}
	// a missing preview must not break the listing
	if u, err := s.Gpu.PresignGet(ctx, *a.Bucket, *a.FileName); err == nil {
		a.PreviewURL = u
	}
}

func (s *Service) Names(ctx context.Context, kind domain.AssetKind) ([]string, error) {
	items, err := s.Assets.ListAssets(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.Name)
	}
	return out, nil
}

type Upload struct {
	URL    string `json:"url"`
	Bucket string `json:"bucket"`
	Object string `json:"object"`
}

// PresignUpload gives the browser a URL to PUT the file to directly.
func (s *Service) PresignUpload(ctx context.Context, kind domain.AssetKind, fileName string) (Upload, error) {
	u, b, o, err := s.Gpu.PresignPut(ctx, kind, path.Base(fileName))
	return Upload{URL: u, Bucket: b, Object: o}, err
}

// Register stores the record after the browser finished uploading to the presigned URL.
func (s *Service) Register(ctx context.Context, kind domain.AssetKind, name string, note *string, bucket, object string) (domain.Asset, error) {
	a, err := s.Assets.CreateAsset(ctx, kind, domain.Asset{Name: name, Note: note, Bucket: &bucket, FileName: &object})
	if err != nil {
		return a, err
	}
	s.fillPreview(ctx, &a)
	return a, nil
}

// ImportBackground fetches a YouTube video on gpu-be and registers it as a background.
func (s *Service) ImportBackground(ctx context.Context, name string, note *string, link string) (domain.Asset, error) {
	if s.Importer == nil {
		return domain.Asset{}, fmt.Errorf("%w: background import is not configured", domain.ErrInvalid)
	}
	if u, err := url.Parse(link); err != nil || (u.Hostname() != "youtu.be" && u.Hostname() != "youtube.com" && !strings.HasSuffix(u.Hostname(), ".youtube.com")) {
		return domain.Asset{}, fmt.Errorf("%w: expected a YouTube video link", domain.ErrInvalid)
	}
	out, err := s.Importer.ImportBackground(ctx, &workflow.ImportBackgroundRequest{URL: link, Name: name})
	if err != nil {
		return domain.Asset{}, err
	}
	a, err := s.Register(ctx, domain.KindBackground, name, note, out.Bucket, out.Object)
	if err != nil {
		_ = s.Gpu.DeleteObject(ctx, out.Bucket, out.Object) // best effort: no record points at the file
	}
	return a, err
}

func (s *Service) Update(ctx context.Context, kind domain.AssetKind, id int64, u domain.AssetUpdate) (domain.Asset, error) {
	a, err := s.Assets.UpdateAsset(ctx, kind, id, u)
	if err == nil {
		s.fillPreview(ctx, &a)
	}
	return a, err
}

func (s *Service) Delete(ctx context.Context, kind domain.AssetKind, id int64) error {
	a, err := s.Assets.GetAsset(ctx, kind, id)
	if err != nil {
		return err
	}
	if err := s.Assets.DeleteAsset(ctx, kind, id); err != nil {
		return err
	}
	if a.Bucket != nil && a.FileName != nil {
		_ = s.Gpu.DeleteObject(ctx, *a.Bucket, *a.FileName) // best effort: record is already gone
	}
	return nil
}

// ── accounts ─────────────────────────────────────────────────────────────────

func (s *Service) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	return s.Accounts.ListAccounts(ctx)
}

func (s *Service) AccountNames(ctx context.Context) (map[string][]string, error) {
	return s.Accounts.AccountNamesByPlatform(ctx)
}

// CreateAccount upserts an account; the optional auth file goes to gpu-be and is never stored here.
func (s *Service) CreateAccount(ctx context.Context, a domain.Account, authFile []byte) (domain.Account, error) {
	if len(authFile) > 0 {
		b, o, err := s.Gpu.PutAuthFile(ctx, a.Platform, a.Name, authFile)
		if err != nil {
			return a, err
		}
		a.Bucket, a.FileName = &b, &o
	}
	return s.Accounts.CreateAccount(ctx, a)
}

func (s *Service) UpdateAccount(ctx context.Context, id int64, u domain.AccountUpdate) (domain.Account, error) {
	return s.Accounts.UpdateAccount(ctx, id, u, nil, nil)
}

func (s *Service) DeleteAccount(ctx context.Context, id int64) error {
	a, err := s.Accounts.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Accounts.DeleteAccount(ctx, id); err != nil {
		return err
	}
	if a.Bucket != nil && a.FileName != nil {
		_ = s.Gpu.DeleteObject(ctx, *a.Bucket, *a.FileName)
	}
	return nil
}
