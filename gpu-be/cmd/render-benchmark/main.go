// render-benchmark compares the production old/new pipelines on identical local
// source bytes. It never contacts the coordinator, database or publishing API.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/ffmpeg"
	"terex/gpu-be/internal/repository/mediaguard"
	"terex/gpu-be/internal/service/renderer"
	"terex/proto/workflow"
)

type clip struct {
	Path    string  `json:"path"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Banner  string  `json:"banner,omitempty"`
	Channel string  `json:"channel,omitempty"`
}
type manifest struct {
	Shorts   []clip `json:"shorts"`
	Assembly []clip `json:"assembly"`
}
type result struct {
	Profile          string  `json:"profile"`
	Case             string  `json:"case"`
	Iteration        int     `json:"iteration"`
	DurationMS       int64   `json:"duration_ms"`
	CPUSeconds       float64 `json:"cpu_seconds"`
	MaxRSSKiB        int64   `json:"max_rss_kib"`
	PeakScratchBytes int64   `json:"peak_scratch_bytes"`
	FFmpegPasses     int     `json:"ffmpeg_passes"`
	StorageGets      int     `json:"storage_gets"`
	StoragePuts      int     `json:"storage_puts"`
	StorageGetBytes  int64   `json:"storage_get_bytes"`
	StoragePutBytes  int64   `json:"storage_put_bytes"`
}
type sample struct {
	mu     sync.Mutex
	cpu    float64
	rss    int64
	passes int
}
type measuredLog struct {
	slog.Handler
	sample *sample
}

func (h measuredLog) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "ffmpeg finished" || r.Message == "ffmpeg failed" {
		h.sample.mu.Lock()
		h.sample.passes++
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "cpu_user_seconds", "cpu_system_seconds":
				h.sample.cpu += a.Value.Float64()
			case "max_rss_kib":
				h.sample.rss = max(h.sample.rss, a.Value.Int64())
			}
			return true
		})
		h.sample.mu.Unlock()
	}
	return h.Handler.Handle(ctx, r)
}

// localStore copies files to simulate GET/PUT byte counts without modifying a
// remote bucket. Those counters are expected transfer volume, not network time.
type localStore struct {
	root               string
	paths              map[domain.ObjectRef]string
	gets, puts         int
	getBytes, putBytes int64
}

func copyFile(src, dest string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	return n, err
}
func (s *localStore) Download(ctx context.Context, r domain.ObjectRef, p string) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	src, ok := s.paths[r]
	if !ok {
		return fmt.Errorf("fixture object absent: %s/%s", r.Bucket, r.Object)
	}
	n, err := copyFile(src, p)
	s.gets++
	s.getBytes += n
	return err
}
func (s *localStore) Upload(ctx context.Context, r domain.ObjectRef, p string) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	// Object names originate from this program, never from a fixture path.
	dest := filepath.Join(s.root, r.Bucket, filepath.FromSlash(r.Object))
	n, err := copyFile(p, dest)
	s.puts++
	s.putBytes += n
	if err == nil {
		s.paths[r] = dest
	}
	return err
}
func (*localStore) Delete(context.Context, domain.ObjectRef) error                { return nil }
func (*localStore) Put(context.Context, domain.ObjectRef, io.Reader, int64) error { return nil }
func (*localStore) PresignGet(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}
func (*localStore) PresignPut(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}

func hashFile(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func scratchBytes(roots ...string) int64 {
	var n int64
	for _, root := range roots {
		_ = filepath.Walk(root, func(_ string, i os.FileInfo, e error) error {
			if e == nil && i.Mode().IsRegular() {
				n += i.Size()
			}
			return nil
		})
	}
	return n
}

func runCase(ctx context.Context, root, profile, name string, iteration int, clips []clip, assembly, nvenc bool) (out result, err error) {
	dir, err := os.MkdirTemp(root, "case-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	work, asm := filepath.Join(dir, "work"), filepath.Join(dir, "assembly")
	guard := mediaguard.New([]string{work, asm}, 10<<30, 2<<30)
	if err = guard.Prepare(); err != nil {
		return out, err
	}
	watch, cancel := context.WithCancel(ctx)
	defer cancel()
	go guard.Run(watch)
	metrics := &sample{}
	slog.SetDefault(slog.New(measuredLog{Handler: slog.NewJSONHandler(os.Stderr, nil), sample: metrics}))
	media := ffmpeg.New(nvenc)
	media.Resources = guard
	store := &localStore{root: filepath.Join(dir, "objects"), paths: map[domain.ObjectRef]string{}}
	svc := &renderer.Service{Store: store, Media: media, TmpDir: work, AssemblyTmpDir: asm, AssemblyBudgetBytes: 8 << 30, Resources: guard, TwitchFastPath: profile == "optimized", AssemblyLocalReuse: profile == "optimized"}
	w := workflow.RenderWork{JobID: "benchmark", ClipID: name, RevisionID: 1, Twitch: true, Output: domain.ObjectRef{Bucket: "clips", Object: "attempt.mp4"}}
	if assembly {
		a := &workflow.AssemblySpec{SchemaVersion: 2, Profile: "landscape_1080p30", BumperDurationFrames: 30}
		for i, c := range clips {
			r := domain.ObjectRef{Bucket: "imports", Object: fmt.Sprintf("verified/fixture-%d/source.mp4", i)}
			store.paths[r] = c.Path
			hash, e := hashFile(c.Path)
			if e != nil {
				return out, e
			}
			a.Items = append(a.Items, workflow.AssemblyItem{MediaID: fmt.Sprint(i), TwitchSourceID: fmt.Sprint(i), Original: r, SHA256: hash, StartSec: c.Start, EndSec: c.End, BroadcasterLogin: c.Channel})
		}
		w.Source.Assembly = a
		var release func()
		ctx, release, err = svc.BeginAssembly(ctx)
		if err != nil {
			return out, err
		}
		defer release()
	} else {
		c := clips[0]
		r := domain.ObjectRef{Bucket: "imports", Object: "verified/fixture/source.mp4"}
		store.paths[r] = c.Path
		probe, e := media.Probe(ctx, c.Path)
		if e != nil {
			return out, e
		}
		w.StartSec, w.EndSec = c.Start, c.End
		w.Source = workflow.SourceManifest{Original: r, DurationSec: probe.DurationSec, Options: domain.JobOptions{Vertical: true, Shape: "rectangle"}}
		if c.Banner != "" {
			w.Source.Banner = "fixture" + filepath.Ext(c.Banner)
			store.paths[domain.ObjectRef{Bucket: domain.BucketBanners, Object: w.Source.Banner}] = c.Banner
		}
	}
	var peak int64
	stopScan := make(chan struct{})
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopScan:
				return
			case <-ticker.C:
				peak = max(peak, scratchBytes(work, asm))
			}
		}
	}()
	start := time.Now()
	prepared, e := svc.PrepareRender(ctx, w)
	if e == nil {
		_, e = svc.RenderPrepared(ctx, prepared)
		peakNow := scratchBytes(work, asm)
		close(stopScan)
		<-scanDone
		peak = max(peak, peakNow)
		prepared.Close()
	} else {
		close(stopScan)
		<-scanDone
	}
	if e != nil {
		return out, e
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	return result{Profile: profile, Case: name, Iteration: iteration, DurationMS: time.Since(start).Milliseconds(), CPUSeconds: metrics.cpu, MaxRSSKiB: metrics.rss, PeakScratchBytes: peak, FFmpegPasses: metrics.passes, StorageGets: store.gets, StoragePuts: store.puts, StorageGetBytes: store.getBytes, StoragePutBytes: store.putBytes}, nil
}

func main() {
	input := flag.String("manifest", "", "JSON with six shorts and a 5–8 minute assembly")
	root := flag.String("scratch-root", "", "existing dedicated scratch parent (default OS temp)")
	nvenc := flag.Bool("nvenc", false, "use the production NVIDIA encoder/decoder")
	iterations := flag.Int("iterations", 3, "sequential repetitions per profile")
	profile := flag.String("profile", "both", "legacy, optimized, or both")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *input, *root, *profile, *iterations, *nvenc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, input, root, profile string, iterations int, nvenc bool) error {
	if input == "" || iterations < 1 {
		return fmt.Errorf("manifest and positive iterations required")
	}
	bytes, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var m manifest
	if err = json.Unmarshal(bytes, &m); err != nil {
		return err
	}
	if len(m.Shorts) != 6 || len(m.Assembly) < 3 || len(m.Assembly) > 60 {
		return fmt.Errorf("provide exactly six shorts and 3–60 assembly items")
	}
	var duration float64
	for _, c := range append(append([]clip{}, m.Shorts...), m.Assembly...) {
		if c.Path == "" || c.Start < 0 || c.End <= c.Start {
			return fmt.Errorf("invalid source interval")
		}
	}
	for _, c := range m.Assembly {
		duration += c.End - c.Start
	}
	duration += float64(len(m.Assembly) - 1)
	if duration < 300 || duration > 480 {
		return fmt.Errorf("assembly with transitions must be 300–480 seconds (got %.2f)", duration)
	}
	profiles := []string{profile}
	if profile == "both" {
		profiles = []string{"legacy", "optimized"}
	} else if profile != "legacy" && profile != "optimized" {
		return fmt.Errorf("unknown profile")
	}
	owned, err := os.MkdirTemp(root, "terex-render-benchmark-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(owned)
	encoder := json.NewEncoder(os.Stdout)
	// Log hashes before any rendering, so comparisons can demonstrate identical
	// inputs even when a source path happens to be reused in multiple cases.
	hashes := map[string]string{}
	probes := map[string]domain.MediaProbe{}
	media := ffmpeg.New(false)
	for _, c := range append(append([]clip{}, m.Shorts...), m.Assembly...) {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		p, ok := probes[c.Path]
		if !ok {
			p, err = media.Probe(ctx, c.Path)
			if err != nil {
				return err
			}
			probes[c.Path] = p
		}
		if c.End > p.DurationSec+0.05 {
			return fmt.Errorf("fixture interval exceeds %s duration %.3f", c.Path, p.DurationSec)
		}
		for _, p := range []string{c.Path, c.Banner} {
			if p != "" {
				if _, ok := hashes[p]; ok {
					continue
				}
				hash, e := hashFile(p)
				if e != nil {
					return e
				}
				hashes[p] = hash
			}
		}
	}
	if err = encoder.Encode(map[string]any{"fixture_sha256": hashes, "nvenc": nvenc, "threads": 2, "storage_counters": "simulated transfer bytes; local copy timing", "max_rss": "peak individual FFmpeg process, not combined cgroup RSS"}); err != nil {
		return err
	}
	for n := 1; n <= iterations; n++ {
		for _, p := range profiles {
			for i, c := range m.Shorts {
				r, e := runCase(ctx, owned, p, fmt.Sprintf("short-%d", i+1), n, []clip{c}, false, nvenc)
				if e != nil {
					return e
				}
				if e = encoder.Encode(r); e != nil {
					return e
				}
			}
			r, e := runCase(ctx, owned, p, "assembly", n, m.Assembly, true, nvenc)
			if e != nil {
				return e
			}
			if e = encoder.Encode(r); e != nil {
				return e
			}
		}
	}
	return nil
}
