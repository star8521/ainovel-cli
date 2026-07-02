package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

func newPausePointFixture(t *testing.T, phase domain.Phase) (*SavePausePointTool, *store.Store) {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if phase != "" {
		if err := s.Progress.Save(&domain.Progress{Phase: phase}); err != nil {
			t.Fatalf("save progress: %v", err)
		}
	}
	return NewSavePausePointTool(s), s
}

func TestSavePausePointSetAndPersist(t *testing.T) {
	tool, s := newPausePointFixture(t, domain.PhaseWriting)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"after":"rewrites_drained","reason":"重写第3章，语气改冷"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(out, &result)
	if result["pause_point_set"] != true || result["after"] != domain.PauseAfterRewritesDrained {
		t.Fatalf("unexpected result: %v", result)
	}

	meta, _ := s.RunMeta.Load()
	if meta.PausePoint == nil || meta.PausePoint.Reason != "重写第3章，语气改冷" || meta.PausePoint.SetAt == "" {
		t.Fatalf("pause point not persisted: %+v", meta.PausePoint)
	}
}

func TestSavePausePointRejectsOutsideWriting(t *testing.T) {
	for _, phase := range []domain.Phase{domain.PhaseComplete, domain.PhaseOutline} {
		tool, _ := newPausePointFixture(t, phase)
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"after":"rewrites_drained"}`))
		if !errors.Is(err, errs.ErrToolPrecondition) {
			t.Errorf("phase %s: expected precondition error, got %v", phase, err)
		}
	}
	// progress 未初始化同样拒绝
	tool, _ := newPausePointFixture(t, "")
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"after":"rewrites_drained"}`)); !errors.Is(err, errs.ErrToolPrecondition) {
		t.Errorf("nil progress: expected precondition error, got %v", err)
	}
}

func TestSavePausePointRejectsUnknownAfter(t *testing.T) {
	tool, _ := newPausePointFixture(t, domain.PhaseWriting)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"after":"volume_end"}`)); !errors.Is(err, errs.ErrToolArgs) {
		t.Errorf("expected args error, got %v", err)
	}
}

func TestSavePausePointCancelIdempotent(t *testing.T) {
	tool, s := newPausePointFixture(t, domain.PhaseWriting)

	// 无点可取消：返回事实 false，不报错
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"cancel":true}`))
	if err != nil {
		t.Fatalf("cancel on empty: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(out, &result)
	if result["pause_point_cleared"] != false {
		t.Fatalf("expected cleared=false, got %v", result)
	}

	// 设点后取消
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"after":"rewrites_drained"}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"cancel":true}`))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_ = json.Unmarshal(out, &result)
	if result["pause_point_cleared"] != true {
		t.Fatalf("expected cleared=true, got %v", result)
	}
	if meta, _ := s.RunMeta.Load(); meta.PausePoint != nil {
		t.Fatal("pause point should be cleared")
	}
}
