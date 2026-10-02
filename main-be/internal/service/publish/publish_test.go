package publish

import (
	"context"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
)

type fakeClips struct {
	domain.ClipRepo
	saved     []domain.Clip
	approval  map[string]string
	scheduled []string
	mode      string
	due       []domain.DuePublication
	marks     []string
}

func (f *fakeClips) SaveClips(_ context.Context, _ string, c []domain.Clip) error {
	f.saved = append(f.saved, c...)
	return nil
}
func (f *fakeClips) JobApproveMode(context.Context, string) (string, error) { return f.mode, nil }
func (f *fakeClips) SetApproval(_ context.Context, id, a string) error {
	if f.approval == nil {
		f.approval = map[string]string{}
	}
	f.approval[id] = a
	return nil
}
func (f *fakeClips) SchedulePublications(_ context.Context, id string, _ time.Time) (int, error) {
	f.scheduled = append(f.scheduled, id)
	return 1, nil
}
func (f *fakeClips) DuePublications(context.Context, time.Time, int) ([]domain.DuePublication, error) {
	return f.due, nil
}
func (f *fakeClips) MarkPublication(_ context.Context, clip string, _ int64, status, _, _ string) error {
	f.marks = append(f.marks, clip+":"+status)
	return nil
}

type fakeJobs struct {
	domain.JobRepo
	status string
}

func (f *fakeJobs) SetJobStatus(_ context.Context, _, s, _ string) error { f.status = s; return nil }

type fakeBroker struct {
	queues []string
	err    error
}

func (b *fakeBroker) Publish(_ context.Context, q string, _ any) error {
	b.queues = append(b.queues, q)
	return b.err
}

func ready() msg.ClipsReady {
	return msg.ClipsReady{JobID: "j1", Clips: []msg.Clip{
		{ClipID: "j1-0-0", Banner: "a.png"}, {ClipID: "j1-0-1", Banner: "a.png"}, {ClipID: "j1-1-0", Banner: ""},
	}}
}

func TestClipsReadyAutoApproveSchedulesAll(t *testing.T) {
	c, j := &fakeClips{mode: domain.ApproveAuto}, &fakeJobs{}
	m := &Manager{Clips: c, Jobs: j}
	if err := m.HandleClipsReady(context.Background(), ready()); err != nil {
		t.Fatal(err)
	}
	if j.status != "done" || len(c.scheduled) != 3 {
		t.Fatalf("status=%s scheduled=%v", j.status, c.scheduled)
	}
	// idx restarts per banner variant
	if c.saved[1].Idx != 1 || c.saved[2].Idx != 0 {
		t.Fatalf("bad idx: %+v", c.saved)
	}
}

func TestClipsReadyByApproveWaits(t *testing.T) {
	c := &fakeClips{mode: domain.ApproveBy}
	m := &Manager{Clips: c, Jobs: &fakeJobs{}}
	if err := m.HandleClipsReady(context.Background(), ready()); err != nil {
		t.Fatal(err)
	}
	if len(c.scheduled) != 0 || len(c.approval) != 0 {
		t.Fatalf("must wait for approval: %+v %+v", c.scheduled, c.approval)
	}
	if err := m.Approve(context.Background(), "j1-0-0"); err != nil || c.approval["j1-0-0"] != domain.ClipApproved || len(c.scheduled) != 1 {
		t.Fatalf("approve failed: %v %+v", err, c)
	}
}

func TestTickRequeuesWhenBrokerFails(t *testing.T) {
	c := &fakeClips{due: []domain.DuePublication{{ClipID: "c1", AccountID: 7, Platform: "youtube", PublishAt: time.Now()}}}
	b := &fakeBroker{err: context.DeadlineExceeded}
	m := &Manager{Clips: c, Broker: b}
	if err := m.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.marks) != 1 || c.marks[0] != "c1:"+domain.PubScheduled {
		t.Fatalf("expected publication returned to scheduled, got %v", c.marks)
	}
}

func TestHandleResultMapsStatus(t *testing.T) {
	c := &fakeClips{}
	m := &Manager{Clips: c}
	_ = m.HandleResult(context.Background(), msg.PublishResult{ClipID: "a", Status: "scheduled"})
	_ = m.HandleResult(context.Background(), msg.PublishResult{ClipID: "b", Status: "published"})
	_ = m.HandleResult(context.Background(), msg.PublishResult{ClipID: "c", Status: "failed"})
	want := []string{"a:sent", "b:published", "c:failed"}
	for i, w := range want {
		if c.marks[i] != w {
			t.Fatalf("marks=%v want=%v", c.marks, want)
		}
	}
}
