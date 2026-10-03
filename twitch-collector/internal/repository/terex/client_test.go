package terex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
)

func TestImportUsesOnlyPresignedUploadAndGuardsCompletion(t *testing.T) {
	var uploaded atomic.Int32
	var completed atomic.Int32
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/imports":
			if r.Header.Get("Authorization") != "Bearer service-token" {
				t.Error("backend auth missing")
			}
			var req twitch.ImportRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.IdempotencyKey != "twitch:SafeClip" || req.AccountID != 9 || req.Source.URL != "https://clips.twitch.tv/SafeClip" {
				t.Errorf("%+v", req)
			}
			json.NewEncoder(w).Encode(twitch.Import{ImportID: "id", State: "awaiting_upload", UploadURL: base + "/upload?signature=temporary"})
		case "/upload":
			if r.Method != "PUT" || r.Header.Get("Authorization") != "" || r.ContentLength != 4 {
				t.Error("unsafe presigned request")
			}
			data, _ := io.ReadAll(r.Body)
			if string(data) != "data" {
				t.Error("wrong file")
			}
			uploaded.Add(1)
			w.WriteHeader(200)
		case "/api/imports/id/complete":
			completed.Add(1)
			json.NewEncoder(w).Encode(twitch.Import{ImportID: "id", State: "pending"})
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	base = server.URL
	path := filepath.Join(t.TempDir(), "file.mp4")
	os.WriteFile(path, []byte("data"), 0600)
	c := New(base, "service-token")
	calls := 0
	guard := func(context.Context) error {
		calls++
		if calls == 3 {
			return domain.ErrLeaseLost
		}
		return nil
	}
	_, err := c.Import(context.Background(), domain.Clip{ID: "SafeClip"}, domain.Download{Path: path, Size: 4, SHA256: "digest", Duration: 20}, 9, guard)
	if !errors.Is(err, domain.ErrLeaseLost) || uploaded.Load() != 1 || completed.Load() != 0 {
		t.Fatalf("err=%v uploaded=%d completed=%d", err, uploaded.Load(), completed.Load())
	}
}
