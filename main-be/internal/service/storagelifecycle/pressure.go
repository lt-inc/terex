package storagelifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"terex/main-be/internal/domain"
	"time"
)

const minioVolumeSelector = `{namespace="infra",persistentvolumeclaim=~"minio-data-minio-.+"}`
const minioFilesystemSelector = `{job="minio-storage",mountpoint="/data"}`

var errPressureMetricMissing = errors.New("Нет свежих метрик свободного места MinIO; проверьте сбор kubelet_volume_stats или minio-storage /data")

func (s *Service) ConfigurePressure(prometheusURL string, minFreeBytes int64, minFreePercent float64) {
	s.pressureMu.Lock()
	defer s.pressureMu.Unlock()
	s.pressureURL = strings.TrimRight(strings.TrimSpace(prometheusURL), "/")
	s.pressureMinBytes = minFreeBytes
	s.pressureMinPercent = minFreePercent
	if s.pressureMinBytes <= 0 {
		s.pressureMinBytes = 1 << 30
	}
	if s.pressureMinPercent <= 0 || s.pressureMinPercent >= 100 {
		s.pressureMinPercent = 10
	}
	s.pressureLastChecked = time.Time{}
}

// Accept actual filesystem statistics from kubelet or the MinIO /data exporter.
// A PVC's requested size must never be substituted for available disk space.
func pressureQuery(metric, labels string) string {
	selector := metric + labels
	return "min(" + selector + " and (time() - timestamp(" + selector + ") < 180))"
}
func parsePressureValue(body io.Reader) (int64, error) {
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&result); err != nil {
		return 0, fmt.Errorf("Некорректный ответ мониторинга свободного места")
	}
	if result.Status != "success" || result.Data.ResultType != "vector" || len(result.Data.Result) != 1 || len(result.Data.Result[0].Value) != 2 {
		return 0, errPressureMetricMissing
	}
	var value string
	if err := json.Unmarshal(result.Data.Result[0].Value[1], &value); err != nil {
		return 0, fmt.Errorf("Некорректное значение свободного места")
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= math.MaxInt64 {
		return 0, fmt.Errorf("Некорректное значение свободного места")
	}
	return int64(n), nil
}
func fetchPressureValue(ctx context.Context, baseURL, query string) (int64, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return 0, fmt.Errorf("Некорректный адрес Prometheus для контроля хранилища")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("Мониторинг свободного места недоступен")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Мониторинг свободного места вернул HTTP %d", resp.StatusCode)
	}
	return parsePressureValue(resp.Body)
}

func fetchPressurePair(ctx context.Context, baseURL, availableMetric, capacityMetric, labels string) (available, total int64, availableErr, totalErr error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		available, availableErr = fetchPressureValue(ctx, baseURL, pressureQuery(availableMetric, labels))
	}()
	go func() {
		defer wg.Done()
		total, totalErr = fetchPressureValue(ctx, baseURL, pressureQuery(capacityMetric, labels))
	}()
	wg.Wait()
	return
}

// Pressure checks run independently of inventory/cleanup, so a slow listing can
// never keep an old 'ok' status alive while new downloads fill the disk.
func (s *Service) PressureTick(ctx context.Context) error {
	s.pressureMu.Lock()
	defer s.pressureMu.Unlock()
	if !s.pressureLastChecked.IsZero() && time.Since(s.pressureLastChecked) < 30*time.Second {
		return nil
	}
	now := time.Now().UTC()
	pressure := domain.TwitchStoragePressure{Required: s.pressureURL != "", State: "off", ObservedAt: now}
	if pressure.Required {
		pressure.State = "unknown"
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		available, total, availableErr, totalErr := fetchPressurePair(queryCtx, s.pressureURL, "kubelet_volume_stats_available_bytes", "kubelet_volume_stats_capacity_bytes", minioVolumeSelector)
		if errors.Is(availableErr, errPressureMetricMissing) || errors.Is(totalErr, errPressureMetricMissing) {
			// local-path volumes often have no kubelet volume-statistics provider.
			// The fallback exporter is mounted on the SAME MinIO PVC at /data;
			// never substitute a guessed node root filesystem or PVC request.
			available, total, availableErr, totalErr = fetchPressurePair(queryCtx, s.pressureURL, "node_filesystem_avail_bytes", "node_filesystem_size_bytes", minioFilesystemSelector)
		}
		cancel()
		if availableErr != nil {
			pressure.Error = availableErr.Error()
		} else if totalErr != nil {
			pressure.Error = totalErr.Error()
		} else if total <= 0 || available > total {
			pressure.Error = "Мониторинг вернул несогласованные размеры хранилища"
		} else {
			pressure.AvailableBytes = &available
			pressure.TotalBytes = &total
			pressure.State = "ok"
			if available < s.pressureMinBytes || float64(available)/float64(total)*100 < s.pressureMinPercent {
				pressure.State = "low"
				pressure.Reason = fmt.Sprintf("На хранилище осталось %.2f ГиБ (%.1f%%); новые скачивания и рендеры приостановлены", float64(available)/(1<<30), float64(available)/float64(total)*100)
			}
		}
		if pressure.State == "unknown" {
			pressure.Reason = "Не удалось проверить свободное место; новые скачивания и рендеры приостановлены"
		}
	}
	if err := s.Repo.SaveTwitchStoragePressure(ctx, pressure); err != nil {
		return err
	}
	s.pressureLastChecked = time.Now().UTC()
	if pressure.State == "low" || pressure.State == "unknown" {
		slog.WarnContext(ctx, "twitch storage pressure blocks preparation", "state", pressure.State, "reason", pressure.Reason, "error", pressure.Error)
	}
	return nil
}
