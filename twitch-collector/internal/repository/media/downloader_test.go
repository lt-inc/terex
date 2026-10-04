package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
)

const validProbe = `{"streams":[{"codec_type":"video","width":1920,"height":1080},{"codec_type":"audio"}],"format":{"duration":"20"}}`

func TestDownloaderCanonicalAndInspected(t *testing.T) {
	d := New("yt", "probe", t.TempDir())
	d.Run = func(ctx context.Context, bin string, args []string, dir string) ([]byte, error) {
		if bin == "yt" {
			if args[len(args)-1] != "https://clips.twitch.tv/Valid_Clip" {
				t.Fatal(args)
			}
			if err := os.WriteFile(filepath.Join(dir, "source.mp4"), []byte("synthetic-video"), 0600); err != nil {
				t.Fatal(err)
			}
			return nil, nil
		}
		return []byte(validProbe), nil
	}
	f, err := d.Download(context.Background(), "Valid_Clip", 20)
	if err != nil {
		t.Fatal(err)
	}
	if f.Size != 15 || len(f.SHA256) != 64 || f.Width != 1920 || !f.HasAudio {
		t.Fatalf("%+v", f)
	}
	f.Cleanup()
	if _, err = os.Stat(f.Path); !os.IsNotExist(err) {
		t.Fatal("temp file leaked")
	}
	if _, err = d.Download(context.Background(), "https://evil.example", 20); err == nil {
		t.Fatal("arbitrary URL accepted")
	}
}
func TestGuardActuallyCancelsOversizedProcess(t *testing.T) {
	d := New("yt", "probe", t.TempDir())
	d.MaxSize = 8
	d.Run = func(ctx context.Context, bin string, args []string, dir string) ([]byte, error) {
		os.WriteFile(filepath.Join(dir, "source.mp4.part"), make([]byte, 9), 0600)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	started := time.Now()
	_, err := d.Download(context.Background(), "SafeClip", 20)
	var f *domain.Failure
	if !errors.As(err, &f) || f.Kind != "size" || time.Since(started) > time.Second {
		t.Fatal(err)
	}
}
func TestFixedDiagnosticsAndPermanentErrors(t *testing.T) {
	for _, s := range []string{"HTTP Error 403 https://secret?token=private", "404 unavailable", "captcha"} {
		f := classify([]byte(s))
		if f.Retryable || strings.Contains(f.Message, "private") {
			t.Fatalf("%+v", f)
		}
	}
	if !classify([]byte("connection reset by peer")).Retryable {
		t.Fatal("network failure not retried")
	}
	if _, err := inspect([]byte(`{"streams":[],"format":{"duration":"20"}}`), 20); err == nil {
		t.Fatal("audio-only payload accepted")
	}
	if _, err := inspect([]byte(validProbe), 30); err == nil {
		t.Fatal("duration mismatch accepted")
	}
}
