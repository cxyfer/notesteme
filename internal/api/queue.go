package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
)

// QueueClass 是一個資源類別的並行設定與目前的負載（PIPE-R8、PIPE-R10）。
type QueueClass struct {
	Class   string `json:"class" doc:"資源類別：download、asr、llm、frames"`
	Limit   int    `json:"limit" doc:"並行上限"`
	Running int    `json:"running" doc:"正在執行（已被領取）的任務數"`
	Pending int    `json:"pending" doc:"等待領取的任務數"`
	Paused  bool   `json:"paused" doc:"這個類別是否單獨暫停；整個佇列暫停時不一定是 true，看最上層的 paused"`
}

// QueueTask 是佇列中還沒結束的任務。
type QueueTask struct {
	ID            string     `json:"id"`
	ItemID        string     `json:"item_id"`
	Item          string     `json:"item" doc:"影音的短代號，例如 yt:abc123"`
	Stage         string     `json:"stage"`
	ResourceClass string     `json:"resource_class"`
	State         string     `json:"state" enum:"leased,pending"`
	Attempts      int        `json:"attempts"`
	NotBefore     *time.Time `json:"not_before" doc:"重試或等待時，最早可以再被領取的時間"`
}

// QueueStatus 是佇列的狀態。tasks 先列已被領取的，再列 pending 的，pending 的順序就是之後領取的順序。
type QueueStatus struct {
	Paused  bool         `json:"paused" doc:"整個佇列是否暫停"`
	Classes []QueueClass `json:"classes"`
	Tasks   []QueueTask  `json:"tasks"`
}

type queueOutput struct{ Body QueueStatus }

func (rt *routes) registerQueue(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "queue-get", Method: http.MethodGet, Path: "/api/queue",
		Summary: "佇列的狀態：各資源類別的上限與負載，以及還沒結束的任務",
	}, rt.getQueue)
	huma.Register(api, huma.Operation{
		OperationID: "queue-limits", Method: http.MethodPatch, Path: "/api/queue/limits",
		Summary: "調整資源類別的並行上限，只改有給的類別；下一次領取就生效（PIPE-R8）",
	}, rt.patchQueueLimits)
	huma.Register(api, huma.Operation{
		OperationID: "queue-pause", Method: http.MethodPost, Path: "/api/queue/pause",
		Summary: "暫停整個佇列，或 body 指定的資源類別；執行中的任務會做完（PIPE-R10）",
	}, rt.pauseQueue)
	huma.Register(api, huma.Operation{
		OperationID: "queue-resume", Method: http.MethodPost, Path: "/api/queue/resume",
		Summary: "恢復整個佇列，或 body 指定的資源類別",
	}, rt.resumeQueue)
}

func errQueueUnavailable() *Error {
	return newError(http.StatusNotImplemented, CodeNotImplemented, "佇列尚未啟用。")
}

// queueStatus 彙整設定（記憶體中的副本）與資料庫中還沒結束的任務。
func (rt *routes) queueStatus(ctx context.Context) (QueueStatus, error) {
	q := rt.d.Queue
	entries, err := q.Entries(ctx)
	if err != nil {
		return QueueStatus{}, err
	}
	ctl := q.Control().Status()
	out := QueueStatus{Paused: ctl.Paused, Classes: make([]QueueClass, 0, len(ctl.Classes)), Tasks: make([]QueueTask, 0, len(entries))}
	running, pending := map[string]int{}, map[string]int{}
	for _, e := range entries {
		if e.State == "leased" {
			running[e.ResourceClass]++
		} else {
			pending[e.ResourceClass]++
		}
		out.Tasks = append(out.Tasks, QueueTask{
			ID: e.ID, ItemID: e.ItemID, Item: source.Ref(e.Platform, e.ExternalID, e.Part), Stage: e.Stage,
			ResourceClass: e.ResourceClass, State: e.State, Attempts: e.Attempts, NotBefore: e.NotBefore,
		})
	}
	for _, c := range ctl.Classes {
		out.Classes = append(out.Classes, QueueClass{
			Class: c.Class, Limit: c.Limit, Running: running[c.Class], Pending: pending[c.Class], Paused: c.Paused,
		})
	}
	return out, nil
}

func (rt *routes) getQueue(ctx context.Context, _ *struct{}) (*queueOutput, error) {
	if rt.d.Queue == nil {
		return nil, errQueueUnavailable()
	}
	return rt.queueResponse(ctx)
}

// queueResponse 回傳目前的佇列狀態；設定改變後的回應也用它，呼叫端立刻看到生效的結果。
func (rt *routes) queueResponse(ctx context.Context) (*queueOutput, error) {
	st, err := rt.queueStatus(ctx)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "讀取佇列狀態時發生錯誤", "error", err)
		return nil, asError(err)
	}
	return &queueOutput{Body: st}, nil
}

type limitsInput struct {
	Body map[string]int
}

func (rt *routes) patchQueueLimits(ctx context.Context, in *limitsInput) (*queueOutput, error) {
	if rt.d.Queue == nil {
		return nil, errQueueUnavailable()
	}
	if err := rt.d.Queue.Control().SetLimits(ctx, in.Body); err != nil {
		return nil, rt.mapQueueErr(ctx, err)
	}
	return rt.queueResponse(ctx)
}

// classInput 的 body 可以省略：沒有 body 時作用在整個佇列。
// Huma 只有在 Body 是指標時才視為選填。
type classInput struct {
	Body *struct {
		Class string `json:"class,omitempty" doc:"只作用在這個資源類別；省略時作用在整個佇列"`
	}
}

// class 回傳要作用的資源類別；空字串代表整個佇列。
func (in *classInput) class() string {
	if in.Body == nil {
		return ""
	}
	return in.Body.Class
}

func (rt *routes) pauseQueue(ctx context.Context, in *classInput) (*queueOutput, error) {
	if rt.d.Queue == nil {
		return nil, errQueueUnavailable()
	}
	if err := rt.d.Queue.Control().Pause(ctx, in.class()); err != nil {
		return nil, rt.mapQueueErr(ctx, err)
	}
	return rt.queueResponse(ctx)
}

func (rt *routes) resumeQueue(ctx context.Context, in *classInput) (*queueOutput, error) {
	if rt.d.Queue == nil {
		return nil, errQueueUnavailable()
	}
	if err := rt.d.Queue.Control().Resume(ctx, in.class()); err != nil {
		return nil, rt.mapQueueErr(ctx, err)
	}
	return rt.queueResponse(ctx)
}

// mapQueueErr 把佇列設定的錯誤對應成 HTTP 錯誤：不認識的類別與不合法的上限是 422，其他是內部錯誤。
func (rt *routes) mapQueueErr(ctx context.Context, err error) error {
	if errors.Is(err, queue.ErrUnknownClass) || errors.Is(err, queue.ErrInvalidLimit) {
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error()+"。")
	}
	rt.d.Log.ErrorContext(ctx, "調整佇列設定時發生錯誤", "error", err)
	return asError(err)
}
