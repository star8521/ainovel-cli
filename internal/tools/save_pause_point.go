package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SavePausePointTool 登记/取消用户验收停靠点（仅 Coordinator 持有）。
// 用户干预"只改某几章、未表达继续写"时，Coordinator 先调本工具再派 editor 入队；
// 重写队列排空后 Host 在流程边界消费停靠点并暂停等待验收（一次性）。
// 停靠点是用户级运行意图，落 meta/run.json 而非创作事实层。
type SavePausePointTool struct {
	store *store.Store
}

func NewSavePausePointTool(s *store.Store) *SavePausePointTool {
	return &SavePausePointTool{store: s}
}

func (t *SavePausePointTool) Name() string  { return "save_pause_point" }
func (t *SavePausePointTool) Label() string { return "验收停靠点" }

func (t *SavePausePointTool) Description() string {
	return "登记用户的验收停靠点：用户要求修改已写章节且未表达继续写意图时，先调本工具（after=rewrites_drained）再派 editor 入队，" +
		"重写全部完成后运行会自动暂停等用户验收。用户明确要求\"改完继续写\"则不要调用；" +
		"用户中途要求继续创作时用 cancel=true 取消已登记的停靠点。完本返工（reopen_book）不需要停靠点。"
}

// 写工具，禁止并发。
func (t *SavePausePointTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SavePausePointTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SavePausePointTool) ActivityDescription(_ json.RawMessage) string {
	return "设置验收停靠点"
}

func (t *SavePausePointTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("after", schema.String("触发条件，目前仅支持 rewrites_drained（重写队列排空后暂停）；cancel=true 时可省略")),
		schema.Property("reason", schema.String("用户诉求摘要（用于暂停提示，如\"重写第3章，语气改冷\"）")),
		schema.Property("cancel", schema.Bool("取消已登记的停靠点（幂等）")),
	)
}

func (t *SavePausePointTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		After  string `json:"after"`
		Reason string `json:"reason"`
		Cancel bool   `json:"cancel"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}

	if a.Cancel {
		meta, err := t.store.RunMeta.Load()
		if err != nil {
			return nil, fmt.Errorf("load run meta: %w: %w", errs.ErrStoreRead, err)
		}
		existed := meta != nil && meta.PausePoint != nil
		if err := t.store.RunMeta.ClearPausePoint(); err != nil {
			return nil, fmt.Errorf("clear pause point: %w: %w", errs.ErrStoreWrite, err)
		}
		return json.Marshal(map[string]any{"pause_point_cleared": existed})
	}

	if a.After != domain.PauseAfterRewritesDrained {
		return nil, fmt.Errorf("after 仅支持 %q，收到 %q: %w", domain.PauseAfterRewritesDrained, a.After, errs.ErrToolArgs)
	}
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil || progress.Phase != domain.PhaseWriting {
		return nil, fmt.Errorf("停靠点仅在写作期（phase=writing）可设；完本返工走 reopen_book，不需要停靠点: %w", errs.ErrToolPrecondition)
	}
	pp := domain.PausePoint{
		After:  a.After,
		Reason: a.Reason,
		SetAt:  time.Now().Format(time.RFC3339),
	}
	if err := t.store.RunMeta.SetPausePoint(pp); err != nil {
		return nil, fmt.Errorf("set pause point: %w: %w", errs.ErrStoreWrite, err)
	}
	return json.Marshal(map[string]any{
		"pause_point_set": true,
		"after":           pp.After,
		"reason":          pp.Reason,
	})
}
