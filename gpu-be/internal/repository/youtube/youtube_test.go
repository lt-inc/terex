package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
)

func videoFile(t *testing.T, n int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(p, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSelectedVisibilityRequiresReadback(t *testing.T) {
	for _, visibility := range []string{"unlisted", "private"} {
		t.Run(visibility, func(t *testing.T) {
			privacy := "public"
			updates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					var wire struct {
						Status struct {
							Privacy string `json:"privacyStatus"`
						} `json:"status"`
					}
					if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
						t.Error(err)
					}
					privacy = wire.Status.Privacy
					updates++
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"items":[{"id":"video","status":{"privacyStatus":%q,"uploadStatus":"processed"},"processingDetails":{"processingStatus":"succeeded"}}]}`, privacy)
			}))
			defer server.Close()
			if err := client(server).SetVisibility(context.Background(), "video", visibility); err != nil {
				t.Fatal(err)
			}
			if privacy != visibility || updates != 1 {
				t.Fatal(privacy, updates)
			}
		})
	}
}

func TestHashtagsAreVisibleInDescriptionAndRespectByteLimit(t *testing.T) {
	m := domain.PublicationMetadata{Title: "Title", Description: "Text #Existing", Tags: []string{"#Existing", "clip", "clip", "two words"}}
	n := youtubeMetadata(m)
	if n.Description != "Text #Existing\n\n#clip #twowords" || strings.Join(n.Tags, ",") != "Existing,clip,two words" {
		t.Fatalf("hashtags lost: %+v", n)
	}
	if err := New(nil).Validate(domain.PublicationMetadata{Title: "Title", Description: strings.Repeat("a", 5000), Tags: []string{"clip"}}); err == nil {
		t.Fatal("hashtags exceeded the description limit")
	}
}
func client(server *httptest.Server) *Client {
	c := New(func(context.Context) (string, error) { return "test-token", nil })
	c.HTTP = server.Client()
	c.APIURL = server.URL
	c.UploadURL = server.URL + "/upload"
	c.ChunkSize = 256 * 1024
	return c
}

func TestResumableUploadServerOffsetAndFinalCheckpoint(t *testing.T) {
	const size = 512*1024 + 3
	var server *httptest.Server
	var ranges []string
	finalFlag := false
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing OAuth")
		}
		if r.Method == "POST" {
			var metadata map[string]any
			_ = json.NewDecoder(r.Body).Decode(&metadata)
			if metadata["status"].(map[string]any)["privacyStatus"] != "private" {
				t.Error("upload must be private")
			}
			w.Header().Set("Location", server.URL+"/session")
			return
		}
		rangeHeader := r.Header.Get("Content-Range")
		if rangeHeader == fmt.Sprintf("bytes */%d", size) {
			w.Header().Set("Range", "bytes=0-262143")
			w.WriteHeader(308)
			return
		}
		ranges = append(ranges, rangeHeader)
		b, _ := io.ReadAll(r.Body)
		if len(ranges) == 1 {
			if len(b) != 256*1024 {
				t.Errorf("chunk size %d", len(b))
			}
			w.Header().Set("Range", "bytes=0-524287")
			w.WriteHeader(308)
			return
		}
		if !finalFlag {
			t.Error("last bytes sent before final-request checkpoint")
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"remote-video"}`))
	}))
	defer server.Close()
	c := client(server)
	s, err := c.Prepare(context.Background(), domain.PublicationMetadata{Title: "Title"}, size)
	if err != nil {
		t.Fatal(err)
	}
	s, err = c.Transfer(context.Background(), videoFile(t, size), s, func(s domain.UploadSession) error {
		if s.FinalRequestStarted {
			finalFlag = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.VideoID != "remote-video" || s.Offset != size {
		t.Fatalf("unexpected completion %+v", s)
	}
	if len(ranges) != 2 || ranges[0] != "bytes 262144-524287/524291" || ranges[1] != "bytes 524288-524290/524291" {
		t.Fatalf("ranges: %v", ranges)
	}
}

func TestPersistFailurePreventsFinalBytes(t *testing.T) {
	chunks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Content-Range"), "*") {
			w.WriteHeader(308)
			return
		}
		chunks++
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"unexpected"}`))
	}))
	defer server.Close()
	_, err := client(server).Transfer(context.Background(), videoFile(t, 3), domain.UploadSession{URI: server.URL, Size: 3}, func(s domain.UploadSession) error {
		if s.FinalRequestStarted {
			return errors.New("database unavailable")
		}
		return nil
	})
	if err == nil || chunks != 0 {
		t.Fatalf("err=%v chunks=%d", err, chunks)
	}
}

func TestLostCompletionRecoveredByProbe(t *testing.T) {
	chunks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || r.Header.Get("Content-Range") != "bytes */3" {
			chunks++
		}
		_, _ = w.Write([]byte(`{"id":"already-uploaded"}`))
	}))
	defer server.Close()
	s, err := client(server).Transfer(context.Background(), videoFile(t, 3), domain.UploadSession{URI: server.URL, Size: 3, FinalRequestStarted: true}, func(domain.UploadSession) error { return nil })
	if err != nil || s.VideoID != "already-uploaded" || chunks != 0 {
		t.Fatalf("session=%+v err=%v chunks=%d", s, err, chunks)
	}
}

func TestExpiredSessionIsUncertain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer server.Close()
	_, err := client(server).Transfer(context.Background(), videoFile(t, 3), domain.UploadSession{URI: server.URL, Size: 3}, func(domain.UploadSession) error { return nil })
	if !errors.Is(err, domain.ErrUploadUncertain) {
		t.Fatalf("err=%v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTransportErrorHidesSessionAndPreservesCancellation(t *testing.T) {
	c := New(func(context.Context) (string, error) { return "token", nil })
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, context.Canceled })}
	_, err := c.Transfer(context.Background(), videoFile(t, 3), domain.UploadSession{URI: c.UploadURL + "?upload_id=secret-upload-id", Size: 3}, func(domain.UploadSession) error { return nil })
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secret-upload-id") || strings.Contains(err.Error(), "googleapis") {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

func TestProgress308LocationIsNotFollowed(t *testing.T) {
	calls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.ContentLength == 0 {
			w.Header().Set("Location", server.URL+"/session")
			w.WriteHeader(308)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"v"}`))
	}))
	defer server.Close()
	s, err := client(server).Transfer(context.Background(), videoFile(t, 3), domain.UploadSession{URI: server.URL + "/session", Size: 3}, func(domain.UploadSession) error { return nil })
	if err != nil || calls != 2 || s.VideoID != "v" {
		t.Fatalf("calls=%d s=%+v err=%v", calls, s, err)
	}
}

func TestMakePublicPreservesStatusAndVerifies(t *testing.T) {
	public := false
	updates := 0
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			var body struct {
				Status map[string]any `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Status["privacyStatus"] != "public" || body.Status["selfDeclaredMadeForKids"] != true || body.Status["containsSyntheticMedia"] != true || body.Status["embeddable"] != false {
				t.Errorf("status not preserved %v", body.Status)
			}
			updates++
			public = true
			_, _ = w.Write([]byte(`{"id":"v"}`))
			return
		}
		reads++
		privacy := "private"
		if public {
			privacy = "public"
		}
		_, _ = fmt.Fprintf(w, `{"items":[{"id":"v","status":{"privacyStatus":%q,"uploadStatus":"processed","selfDeclaredMadeForKids":true,"containsSyntheticMedia":true,"embeddable":false},"processingDetails":{"processingStatus":"succeeded"}}]}`, privacy)
	}))
	defer server.Close()
	if err := client(server).MakePublic(context.Background(), "v"); err != nil {
		t.Fatal(err)
	}
	if updates != 1 || reads != 2 {
		t.Fatalf("updates=%d reads=%d", updates, reads)
	}
}

func TestHTTP200DoesNotProvePublicVisibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			_, _ = w.Write([]byte(`{"id":"v"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"v","status":{"privacyStatus":"private","uploadStatus":"processed"},"processingDetails":{"processingStatus":"succeeded"}}]}`))
	}))
	defer server.Close()
	if err := client(server).MakePublic(context.Background(), "v"); err == nil {
		t.Fatal("must reject unconfirmed visibility")
	}
}
