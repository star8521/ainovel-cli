package host

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

type pauseRecorder struct {
	aborts  []string
	reports []string
}

func newPauseFixture(t *testing.T) (*PausePointSentinel, *storepkg.Store, *pauseRecorder) {
	t.Helper()
	s := storepkg.NewStore(t.TempDir())
	r := &pauseRecorder{}
	sentinel := NewPausePointSentinel(s,
		func(reason string) { r.aborts = append(r.aborts, reason) },
		func(level, summary string) { r.reports = append(r.reports, level+": "+summary) },
	)
	return sentinel, s, r
}

func setPauseProgress(t *testing.T, s *storepkg.Store, phase domain.Phase, pending []int) {
	t.Helper()
	if err := s.Progress.Save(&domain.Progress{Phase: phase, PendingRewrites: pending}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
}

func TestPausePointNilSafe(t *testing.T) {
	var s *PausePointSentinel
	if s.HandleBoundary() {
		t.Error("nil sentinel should not stop")
	}
	s.ReconcileOnResume()
}

func TestPausePointBoundaryKeepsWhileQueueBusy(t *testing.T) {
	sentinel, s, r := newPauseFixture(t)
	_ = s.RunMeta.SetPausePoint(domain.PausePoint{After: domain.PauseAfterRewritesDrained, Reason: "重写第3章"})
	setPauseProgress(t, s, domain.PhaseWriting, []int{3})

	if sentinel.HandleBoundary() {
		t.Fatal("queue busy should not stop")
	}
	if len(r.aborts) != 0 || len(r.reports) != 0 {
		t.Fatalf("no side effects expected, aborts=%v reports=%v", r.aborts, r.reports)
	}
	meta, _ := s.RunMeta.Load()
	if meta.PausePoint == nil {
		t.Fatal("pause point should be kept")
	}
}

func TestPausePointBoundaryStopsOnceWhenDrained(t *testing.T) {
	sentinel, s, r := newPauseFixture(t)
	_ = s.RunMeta.SetPausePoint(domain.PausePoint{After: domain.PauseAfterRewritesDrained, Reason: "重写第3章"})
	setPauseProgress(t, s, domain.PhaseWriting, nil)

	if !sentinel.HandleBoundary() {
		t.Fatal("drained queue should stop")
	}
	if len(r.aborts) != 1 || !strings.Contains(r.aborts[0], "等待验收") || !strings.Contains(r.aborts[0], "重写第3章") {
		t.Fatalf("abort with acceptance summary expected, got %v", r.aborts)
	}
	meta, _ := s.RunMeta.Load()
	if meta.PausePoint != nil {
		t.Fatal("pause point should be consumed")
	}
	// 一次性：再次边界不再触发
	if sentinel.HandleBoundary() {
		t.Fatal("consumed pause point must not fire again")
	}
	if len(r.aborts) != 1 {
		t.Fatalf("expected exactly one abort, got %v", r.aborts)
	}
}

func TestPausePointCompleteConsumesWithoutStop(t *testing.T) {
	sentinel, s, r := newPauseFixture(t)
	_ = s.RunMeta.SetPausePoint(domain.PausePoint{After: domain.PauseAfterRewritesDrained, Reason: "完本返工"})
	setPauseProgress(t, s, domain.PhaseComplete, nil)

	if sentinel.HandleBoundary() {
		t.Fatal("complete phase should not stop the run")
	}
	if len(r.aborts) != 0 {
		t.Fatalf("no abort expected, got %v", r.aborts)
	}
	if len(r.reports) != 1 || !strings.HasPrefix(r.reports[0], "info:") {
		t.Fatalf("expected one info report, got %v", r.reports)
	}
	meta, _ := s.RunMeta.Load()
	if meta.PausePoint != nil {
		t.Fatal("pause point must not survive completion")
	}
}

func TestPausePointReconcileOnResume(t *testing.T) {
	sentinel, s, r := newPauseFixture(t)
	_ = s.RunMeta.SetPausePoint(domain.PausePoint{After: domain.PauseAfterRewritesDrained, Reason: "重写第3章"})

	// 队列未排空：对账不消费
	setPauseProgress(t, s, domain.PhaseWriting, []int{3})
	sentinel.ReconcileOnResume()
	if meta, _ := s.RunMeta.Load(); meta.PausePoint == nil {
		t.Fatal("busy queue reconcile should keep pause point")
	}

	// 停机窗口里条件已满足（排空后消费前；或设点后从未入队——磁盘状态无法区分，
	// 按"显式恢复=放行"一并解除）：Resume 对账消费且不 abort
	setPauseProgress(t, s, domain.PhaseWriting, nil)
	sentinel.ReconcileOnResume()
	if meta, _ := s.RunMeta.Load(); meta.PausePoint != nil {
		t.Fatal("reconcile should consume satisfied pause point")
	}
	if len(r.aborts) != 0 {
		t.Fatalf("reconcile must not abort, got %v", r.aborts)
	}
	if len(r.reports) != 1 || !strings.Contains(r.reports[0], "自动解除") {
		t.Fatalf("expected release report, got %v", r.reports)
	}
}
