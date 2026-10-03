package workflowgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/credentials"
	"terex/gpu-be/internal/service/googleauth"
	"terex/gpu-be/internal/service/renderer"
	"terex/proto/workflow"
)

type Store interface {
	domain.ObjectStore
	Read(context.Context, domain.ObjectRef) ([]byte, error)
	Exists(context.Context, domain.ObjectRef) (bool, error)
}

type Service struct {
	Auth   *googleauth.Service
	Store  Store
	Media  domain.Media
	TmpDir string
}

func (s *Service) OAuthURL(_ context.Context, q *workflow.OAuthURLRequest) (*workflow.OAuthURLResponse, error) {
	if q.AccountID <= 0 {
		return nil, fmt.Errorf("account id is required")
	}
	u, err := s.Auth.BeginWithRedirect(strconv.FormatInt(q.AccountID, 10), q.State, q.RedirectURL)
	return &workflow.OAuthURLResponse{URL: u}, err
}

func connection(c domain.GoogleCredential) *workflow.Connection {
	expiry := c.ExpiresAt
	return &workflow.Connection{Provider: "youtube", RemoteID: c.ChannelID, DisplayName: c.ChannelTitle, CredentialRef: credentials.Ref(c.AccountID), Scopes: strings.Fields(c.Scopes), ExpiresAt: &expiry, Status: "connected"}
}

func (s *Service) OAuthExchange(ctx context.Context, q *workflow.OAuthExchangeRequest) (*workflow.Connection, error) {
	if q.AccountID <= 0 {
		return nil, fmt.Errorf("account id is required")
	}
	if s.Auth.RedirectURL != "" && q.RedirectURL != s.Auth.RedirectURL {
		return nil, fmt.Errorf("OAuth redirect mismatch")
	}
	c, err := s.Auth.ExchangeVerified(ctx, strconv.FormatInt(q.AccountID, 10), q.State, q.Code, q.ExpectedRemoteID)
	if err != nil {
		return nil, err
	}
	return connection(c), nil
}

func credentialID(q *workflow.ConnectionRequest) (string, error) {
	id, err := credentials.ID(q.CredentialRef)
	if err != nil {
		return "", err
	}
	if id != strconv.FormatInt(q.AccountID, 10) {
		return "", fmt.Errorf("credential does not belong to account")
	}
	return id, nil
}

func (s *Service) CheckConnection(ctx context.Context, q *workflow.ConnectionRequest) (*workflow.Connection, error) {
	id, err := credentialID(q)
	if err != nil {
		return nil, err
	}
	c, err := s.Auth.Check(ctx, id)
	if err != nil {
		state := "error"
		if errors.Is(err, domain.ErrReauthentication) {
			state = "needs_reauth"
		}
		return &workflow.Connection{Provider: "youtube", Status: state, CredentialRef: q.CredentialRef, RemoteID: q.ExpectedRemoteID, Error: err.Error()}, nil
	}
	if q.ExpectedRemoteID != "" && q.ExpectedRemoteID != c.ChannelID {
		return &workflow.Connection{Provider: "youtube", Status: "needs_reauth", CredentialRef: q.CredentialRef, RemoteID: q.ExpectedRemoteID, Error: "connected YouTube channel does not match account"}, nil
	}
	return connection(c), nil
}

func (s *Service) Disconnect(ctx context.Context, q *workflow.ConnectionRequest) (*workflow.Result, error) {
	id, err := credentialID(q)
	if err != nil {
		return nil, err
	}
	if err = s.Auth.Disconnect(ctx, id); err != nil {
		return nil, err
	}
	return &workflow.Result{Accepted: true}, nil
}

func (s *Service) EditorContext(ctx context.Context, q *workflow.EditorContextRequest) (*workflow.EditorContextResponse, error) {
	if q.JobID == "" {
		return nil, fmt.Errorf("job id is required")
	}
	m := q.Source
	ref := domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: q.JobID + "/source.json"}
	if m.Original.Object == "" {
		if b, e := s.Store.Read(ctx, ref); e == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	if m.Original.Object == "" {
		m.Original = domain.ObjectRef{Bucket: domain.BucketDownloads, Object: q.JobID + ".mp4"}
		m.Proxy = domain.ObjectRef{Bucket: domain.BucketProxy, Object: q.JobID + ".mp4"}
		m.Transcript = domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: q.JobID + "/transcript.json"}
	}
	available, err := s.Store.Exists(ctx, m.Original)
	if err != nil {
		return nil, err
	}
	cues := m.Cues
	if len(cues) == 0 {
		if b, e := s.Store.Read(ctx, m.Transcript); e == nil {
			var segs []domain.Segment
			if json.Unmarshal(b, &segs) == nil {
				cues = renderer.Cues(segs)
			}
		}
	}
	if len(cues) == 0 {
		if b, e := s.Store.Read(ctx, domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: q.JobID + "/subs.srt"}); e == nil {
			cues = ParseSRT(string(b))
		}
	}
	if m.DurationSec <= 0 && available {
		if prober, ok := s.Media.(interface {
			Duration(context.Context, string) (float64, error)
		}); ok {
			if err = os.MkdirAll(s.TmpDir, 0o755); err != nil {
				return nil, err
			}
			dir, e := os.MkdirTemp(s.TmpDir, "source-probe-")
			if e != nil {
				return nil, e
			}
			defer os.RemoveAll(dir)
			file := filepath.Join(dir, "original.mp4")
			if err = s.Store.Download(ctx, m.Original, file); err != nil {
				return nil, err
			}
			m.DurationSec, err = prober.Duration(ctx, file)
			if err != nil {
				return nil, err
			}
			b, _ := json.Marshal(m)
			if err = s.Store.Put(ctx, ref, bytes.NewReader(b), int64(len(b))); err != nil {
				return nil, err
			}
		}
	}
	u := ""
	preview := m.Proxy
	if preview.Object == "" {
		preview = m.Original
	}
	ok, err := s.Store.Exists(ctx, preview)
	if err != nil {
		return nil, err
	}
	if !ok {
		preview = m.Original
		ok = available
	}
	if ok {
		u, err = s.Store.PresignGet(ctx, preview, time.Hour)
		if err != nil {
			return nil, err
		}
	}
	if q.EndSec > q.StartSec {
		cues = renderer.SelectCues(cues, q.StartSec, q.EndSec)
	}
	m.Cues = cues
	return &workflow.EditorContextResponse{Source: m, SourceURL: u, SourceAvailable: available, Cues: cues}, nil
}

func ParseSRT(text string) []workflow.Cue {
	var out []workflow.Cue
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 3 {
			continue
		}
		parts := strings.Split(lines[1], " --> ")
		if len(parts) != 2 {
			continue
		}
		a, e := srtTime(parts[0])
		if e != nil {
			continue
		}
		b, e := srtTime(parts[1])
		if e != nil || b <= a {
			continue
		}
		out = append(out, workflow.Cue{ID: "srt" + lines[0], Start: a, End: b, Text: strings.Join(lines[2:], "\n")})
	}
	return out
}
func srtTime(text string) (float64, error) {
	var h, m int
	var sec float64
	_, err := fmt.Sscanf(strings.ReplaceAll(strings.TrimSpace(text), ",", "."), "%d:%d:%f", &h, &m, &sec)
	return float64(h*3600+m*60) + sec, err
}
