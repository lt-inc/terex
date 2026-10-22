package jobs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
)

func TestParseScheduleUsesUTC7(t *testing.T) {
	got := ParseSchedule(domain.PublishSchedule{Date: "2026-01-02", Time: "10:30"})
	want := time.Date(2026, 1, 2, 3, 30, 0, 0, time.UTC)
	if got == nil || !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if ParseSchedule(domain.PublishSchedule{Date: "2026-01-02"}) != nil {
		t.Fatal("incomplete schedule must be nil")
	}
}

func TestStepAcceptsNumberAndString(t *testing.T) {
	if step(domain.PublishSchedule{Step: float64(15)}) != 15 || step(domain.PublishSchedule{Step: " 20 "}) != 20 || step(domain.PublishSchedule{}) != 0 {
		t.Fatal("bad step parsing")
	}
}

// recordingJobs captures what Create stores; the unused repository methods stay nil and would panic if called.
type recordingJobs struct {
	domain.JobRepo
	created []domain.NewJob
}

func (r *recordingJobs) CreateJob(_ context.Context, j domain.NewJob) error {
	r.created = append(r.created, j)
	return nil
}
func (r *recordingJobs) JobProcessMessage(context.Context, string) (msg.ProcessVideo, error) {
	return msg.ProcessVideo{}, nil
}
func (r *recordingJobs) SetJobStatus(context.Context, string, string, string) error { return nil }

type nopBroker struct{}

func (nopBroker) Publish(context.Context, string, any) error { return nil }

func TestCreateCarriesTheWatermarkSettings(t *testing.T) {
	region := msg.WatermarkRegion{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}
	cases := []struct {
		name string
		in   *msg.WatermarkOptions
		want *msg.WatermarkOptions
	}{
		{"off", nil, nil},
		{"off mode is dropped", &msg.WatermarkOptions{Mode: "off", Regions: []msg.WatermarkRegion{region}}, nil},
		{"auto", &msg.WatermarkOptions{Mode: msg.WatermarkAuto}, &msg.WatermarkOptions{Mode: msg.WatermarkAuto}},
		{"manual", &msg.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []msg.WatermarkRegion{region}}, &msg.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []msg.WatermarkRegion{region}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := &recordingJobs{}
			s := &Service{Jobs: repo, Broker: nopBroker{}}
			if _, err := s.Create(context.Background(), domain.VideoCard{YoutubeURL: "https://youtu.be/abc", Watermark: c.in}, nil); err != nil {
				t.Fatal(err)
			}
			if len(repo.created) != 1 || !reflect.DeepEqual(repo.created[0].Watermark, c.want) {
				t.Fatalf("stored %+v, want %+v", repo.created, c.want)
			}
		})
	}
}

func TestCreateRejectsABrokenWatermarkBeforeStoring(t *testing.T) {
	for _, w := range []*msg.WatermarkOptions{
		{Mode: msg.WatermarkManual},
		{Mode: "blur"},
		{Mode: msg.WatermarkManual, Regions: []msg.WatermarkRegion{{X: 2, Y: 0, W: 0.1, H: 0.1}}},
	} {
		repo := &recordingJobs{}
		s := &Service{Jobs: repo, Broker: nopBroker{}}
		if _, err := s.Create(context.Background(), domain.VideoCard{YoutubeURL: "https://youtu.be/abc", Watermark: w}, nil); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%+v: err %v, want ErrInvalid", w, err)
		}
		if len(repo.created) != 0 {
			t.Fatalf("%+v: a job with broken watermark settings was stored", w)
		}
	}
}
