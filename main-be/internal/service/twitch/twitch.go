package twitch

import (
	"context"
	"encoding/hex"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"math"
	"net/url"
	"regexp"
	"strings"
	"terex/main-be/internal/domain"
	t "terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

type Service struct {
	Repo    domain.TwitchRepo
	Gateway workflow.IngestGateway
	Gpu     domain.Gpu
	Now     func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

var clipID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}$`)
var broadcasterID = regexp.MustCompile(`^[0-9]{1,32}$`)
var broadcasterLogin = regexp.MustCompile(`^[A-Za-z0-9_]{1,25}$`)

func ValidateImport(r t.ImportRequest, now time.Time) error {
	if (r.SchemaVersion != 1 && r.SchemaVersion != 2) || r.IdempotencyKey == "" || len(r.IdempotencyKey) > 250 || (r.SchemaVersion == 2 && (r.FlowID == "" || r.FlowVersion < 1)) {
		return fmt.Errorf("%w: schema_version=1 and a bounded idempotency_key are required", domain.ErrInvalid)
	}
	src := r.Source
	if src.Platform != "twitch" || !clipID.MatchString(src.ClipID) || !broadcasterID.MatchString(src.BroadcasterID) || !broadcasterLogin.MatchString(src.BroadcasterLogin) || src.CreatedAt.IsZero() || src.CreatedAt.After(now.Add(5*time.Minute)) || src.ViewCount < 0 || math.IsNaN(src.DurationSec) || math.IsInf(src.DurationSec, 0) || src.DurationSec <= 0 || src.DurationSec > 60 {
		return fmt.Errorf("%w: invalid Twitch source metadata", domain.ErrInvalid)
	}
	u, err := url.Parse(src.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return fmt.Errorf("%w: expected an HTTPS Twitch clip URL", domain.ErrInvalid)
	}
	host := strings.ToLower(u.Hostname())
	if host == "clips.twitch.tv" {
		if strings.Trim(u.Path, "/") != src.ClipID {
			return fmt.Errorf("%w: clip URL does not match source ID", domain.ErrInvalid)
		}
	} else if host == "www.twitch.tv" || host == "twitch.tv" {
		if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/clip/"+src.ClipID) {
			return fmt.Errorf("%w: clip URL does not match source ID", domain.ErrInvalid)
		}
	} else {
		return fmt.Errorf("%w: only public Twitch clip URLs are accepted", domain.ErrInvalid)
	}
	if strings.TrimSpace(src.Title) == "" || len([]rune(src.Title)) > 100 {
		return fmt.Errorf("%w: title must contain 1 to 100 characters", domain.ErrInvalid)
	}
	return nil
}
func (s *Service) Settings(ctx context.Context) (t.Settings, error) {
	return s.Repo.TwitchSettings(ctx)
}
func (s *Service) SaveSettings(ctx context.Context, v t.Settings) (t.Settings, error) {
	if err := v.Validate(); err != nil {
		return v, fmt.Errorf("%w: %s", domain.ErrInvalid, err)
	}
	err := s.Repo.SaveTwitchSettings(ctx, v)
	return v, err
}
func (s *Service) Reserve(ctx context.Context, r t.ImportRequest) (t.Import, error) {
	if err := ValidateImport(r, s.now()); err != nil {
		return t.Import{}, err
	}
	var out t.Import
	var err error
	if r.SchemaVersion == 2 {
		var repo domain.TwitchFlowRepo
		repo, err = s.flows()
		if err == nil {
			out, err = repo.ReserveFlowImport(ctx, r, s.now())
		}
	} else {
		out, err = s.Repo.ReserveTwitchImport(ctx, r, s.now())
	}
	if err != nil {
		return out, err
	}
	if out.ClipID != "" || out.Rejected || out.MediaState == "ready" {
		return out, nil
	}
	if s.Gateway == nil {
		return out, fmt.Errorf("ingest gateway is unavailable")
	}
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	uploadID := out.ImportID
	if out.UploadImportID != "" {
		uploadID = out.UploadImportID
	}
	upload, err := s.Gateway.ReserveUpload(timeout, &workflow.ReserveUploadRequest{ImportID: uploadID, Ref: out.Original})
	if err != nil {
		return out, err
	}
	if out.UploadGeneration > 0 {
		repo, _ := s.flows()
		err = repo.SetFlowUpload(ctx, out.ImportID, out.UploadGeneration, upload.Ref, upload.ExpiresAt)
	} else {
		err = s.Repo.SetTwitchUpload(ctx, out.ImportID, upload.Ref, upload.ExpiresAt)
	}
	if err != nil {
		return out, err
	}
	out.Original, out.UploadURL, out.UploadExpiresAt = upload.Ref, upload.UploadURL, &upload.ExpiresAt
	return out, nil
}
func (s *Service) Complete(ctx context.Context, id string, r t.ImportCompleteRequest) (t.Import, error) {
	out, err := s.Repo.GetTwitchImport(ctx, id)
	if err != nil {
		return out, err
	}
	if out.UploadGeneration > 0 && r.UploadGeneration != out.UploadGeneration {
		return out, fmt.Errorf("%w: upload generation changed", domain.ErrConflict)
	}
	if out.ClipID != "" || out.MediaState == "ready" {
		return out, nil
	}
	if out.Rejected {
		return out, fmt.Errorf("%w: import is rejected", domain.ErrConflict)
	}
	hash, err := hex.DecodeString(r.SHA256)
	if err != nil || len(hash) != 32 || r.SizeBytes <= 0 || r.SizeBytes > 512<<20 {
		return out, fmt.Errorf("%w: valid SHA256 and a file of at most 512 MiB are required", domain.ErrInvalid)
	}
	if out.Original.Object == "" {
		return out, fmt.Errorf("%w: media upload has not been reserved", domain.ErrConflict)
	}
	if s.Gateway == nil {
		return out, fmt.Errorf("ingest gateway is unavailable")
	}
	timeout, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	uploadID := id
	if out.UploadImportID != "" {
		uploadID = out.UploadImportID
	}
	media, err := s.Gateway.VerifyUpload(timeout, &workflow.VerifyUploadRequest{ImportID: uploadID, Ref: out.Original, ExpectedSHA256: strings.ToLower(r.SHA256), ExpectedSizeBytes: r.SizeBytes, ExpectedDurationSec: out.Source.DurationSec})
	if err != nil {
		if code := status.Code(err); code == codes.InvalidArgument || code == codes.DataLoss {
			if out.UploadGeneration == 0 {
				_ = s.Repo.FailTwitchImport(ctx, id, "Media verification failed; check the file and retry")
			}
			return out, fmt.Errorf("%w: media verification failed", domain.ErrInvalid)
		}
		if out.UploadGeneration == 0 {
			_ = s.Repo.FailTwitchImport(ctx, id, "Media verification is temporarily unavailable; retry this import")
		}
		return out, fmt.Errorf("media verification is temporarily unavailable: %w", err)
	}
	if out.UploadGeneration > 0 {
		repo, e := s.flows()
		if e != nil {
			return out, e
		}
		return repo.FinishFlowImport(ctx, id, out.UploadGeneration, media, s.now())
	}
	return s.Repo.FinishTwitchImport(ctx, id, media, s.now())
}
func (s *Service) Imports(ctx context.Context, pending bool, state string) ([]t.Import, error) {
	return s.Repo.ListTwitchImports(ctx, pending, state)
}
func (s *Service) Import(ctx context.Context, id string) (t.Import, error) {
	return s.Repo.GetTwitchImport(ctx, id)
}
func (s *Service) Reject(ctx context.Context, id string, rejected bool) (t.Import, error) {
	if err := s.Repo.RejectTwitchImport(ctx, id, rejected); err != nil {
		return t.Import{}, err
	}
	return s.Import(ctx, id)
}
func (s *Service) preview(ctx context.Context, c *domain.Compilation) {
	if c.State == "ready" && c.Object != "" && s.Gpu != nil {
		c.PreviewURL, _ = s.Gpu.PresignGet(ctx, c.Bucket, c.Object)
	}
}
func (s *Service) Compilations(ctx context.Context) ([]domain.Compilation, error) {
	out, err := s.Repo.ListCompilations(ctx)
	return out, err
}
func (s *Service) Compilation(ctx context.Context, id string) (domain.Compilation, error) {
	out, err := s.Repo.GetCompilation(ctx, id)
	return out, err
}
func (s *Service) CreateCompilation(ctx context.Context, r t.CompilationRequest) (domain.Compilation, error) {
	return s.Repo.CreateCompilation(ctx, r, nil, s.now())
}
func (s *Service) UpdateCompilation(ctx context.Context, id string, r domain.CompilationUpdate) (domain.Compilation, error) {
	return s.Repo.UpdateCompilation(ctx, id, r, s.now())
}
func (s *Service) AutoCompilation(ctx context.Context) (domain.AutoCompilationResult, error) {
	out, err := s.Repo.AutoCompilation(ctx, s.now())
	if err != nil {
		return out, err
	}
	switch out.Reason {
	case "created":
		out.Message = "Compilation prepared for review. Rendering and approval are still required."
	case "target_account_missing":
		out.Message = "No compilation created: select an existing YouTube target account in Twitch settings."
	case "collection_disabled":
		out.Message = "No compilation created: Twitch collection is disabled. Enable it in Twitch settings to prepare automatically."
	case "before_scheduled_hour":
		out.Message = fmt.Sprintf("No compilation created: automatic preparation starts at %s (%s). Wait until then, change the compilation hour in Twitch settings, or build manually.", out.ScheduledAt.Format("2006-01-02 15:04"), out.Timezone)
	case "daily_draft_exists":
		out.Message = fmt.Sprintf("No new compilation created: the automatic draft for %s (%s) already exists. Automatic preparation creates at most one draft per local day. Open the existing draft or build another manually.", out.LocalDate, out.Timezone)
	case "insufficient_eligible_sources":
		if counts := out.Sources; counts != nil {
			out.Message = fmt.Sprintf("No compilation created: %d eligible clips; at least %d are required. Of %d imports: %d rejected, %d not ready, %d outside the last %d hours, %d already used in a compilation. Wait for more ready imports, review failed imports, or adjust the window in Twitch settings.", counts.Eligible, out.MinimumClips, counts.Total, counts.Rejected, counts.NotReady, counts.OutsideWindow, out.WindowHours, counts.AlreadyUsed)
		}
	}
	return out, nil
}
func (s *Service) Tick(ctx context.Context) error {
	if repo, ok := s.Repo.(domain.TwitchFlowRepo); ok {
		return repo.TickTwitchFlows(ctx, s.now())
	}
	_, err := s.Repo.AutoCompilation(ctx, s.now())
	return err
}
