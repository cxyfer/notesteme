package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"

	"github.com/cxyfer/notesteme/internal/store"
)

// 資源類別（ADR-0004）：每個類別有自己的並行上限，也能單獨暫停（PIPE-R8、PIPE-R10）。
const (
	ResourceASR    = "asr"
	ResourceLLM    = "llm"
	ResourceFrames = "frames"
)

// Classes 是開放調整與暫停的資源類別。ResourceLocal（normalize 等本機的內部步驟）不在其中。
var Classes = []string{ResourceDownload, ResourceASR, ResourceLLM, ResourceFrames}

// LocalLimit 是 ResourceLocal 的並行上限。它是內部用的，不開放調整，也不能單獨暫停。
const LocalLimit = 4

// DefaultLimit 回傳資源類別的預設並行上限（ADR-0004）：download 2、asr 3、llm 4、frames 為 CPU 核心數。
func DefaultLimit(class string) int {
	switch class {
	case ResourceDownload:
		return 2
	case ResourceASR:
		return 3
	case ResourceLLM:
		return 4
	case ResourceFrames:
		return max(runtime.NumCPU(), 1)
	case ResourceLocal:
		return LocalLimit
	}
	return 0
}

// 控制設定的錯誤；API 把它們對應成 422。
var (
	// ErrUnknownClass 表示資源類別不存在（或不開放調整）。
	ErrUnknownClass = errors.New("不認識的資源類別")
	// ErrInvalidLimit 表示並行上限小於 1 或大於 MaxLimit。
	ErrInvalidLimit = errors.New("並行上限必須介於 1 與 64")
)

// MaxLimit 是並行上限可以設定的最大值，避免一個打錯的數字讓任務數量失控。
const MaxLimit = 64

// controlKey 是控制設定在 settings 表中的 key。
const controlKey = "queue"

// controlState 是存在 settings 表的樣子。只記錄使用者改過的上限；沒有記錄的類別用預設值，
// 這樣預設值（例如 frames 的 CPU 核心數）之後調整時，沒改過的類別會跟著變。
type controlState struct {
	Limits        map[string]int `json:"limits,omitempty"`
	Paused        bool           `json:"paused,omitempty"`
	PausedClasses []string       `json:"paused_classes,omitempty"`
}

// Control 是佇列的控制設定：各資源類別的並行上限與暫停狀態。
// 設定存在 settings 表（重啟後保留），記憶體中有一份副本，領取任務時直接讀副本；
// 修改後的下一次領取就生效（PIPE-R8、PIPE-R10）。並行安全。
type Control struct {
	st   *store.Store
	wake func()

	mu    sync.RWMutex
	state controlState
	// caps 是這個程序內的上限上界（SetCap），不存檔，只影響 Free。
	caps map[string]int
}

func newControl(st *store.Store, wake func()) *Control {
	return &Control{st: st, wake: wake}
}

// Load 讀入存在 settings 表的設定。啟動時呼叫一次；沒有存過就用預設值。
func (c *Control) Load(ctx context.Context) error {
	raw, ok, err := c.st.GetSetting(ctx, controlKey)
	if err != nil {
		return fmt.Errorf("讀取佇列設定：%w", err)
	}
	var st controlState
	if ok {
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			return fmt.Errorf("佇列設定不是合法的 JSON：%w", err)
		}
	}
	c.mu.Lock()
	c.state = st
	c.mu.Unlock()
	return nil
}

// Limit 回傳資源類別目前的並行上限；不認識的類別回傳 0。
func (c *Control) Limit(class string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.limitLocked(class)
}

func (c *Control) limitLocked(class string) int {
	if n, ok := c.state.Limits[class]; ok {
		return n
	}
	return DefaultLimit(class)
}

// SetCap 設定資源類別在這個程序內的上限上界（不存檔）：Free 取 min(設定的上限, cap)，
// Limit 與 Status 仍是設定的上限，所以 GET /api/queue 的顯示不變。n < 1 代表解除。只給 TestControls 使用。
func (c *Control) SetCap(class string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 1 {
		delete(c.caps, class)
	} else {
		if c.caps == nil {
			c.caps = map[string]int{}
		}
		c.caps[class] = n
	}
	// 放寬 cap 之後可能有任務可以領取了。
	if c.wake != nil {
		c.wake()
	}
}

// Paused 回報整個佇列是否暫停。
func (c *Control) Paused() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.Paused
}

// ClassPaused 回報資源類別是否暫停（整個佇列暫停時所有類別都算暫停）。
func (c *Control) ClassPaused(class string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.Paused || slices.Contains(c.state.PausedClasses, class)
}

// Free 依各類別正在執行的任務數（running）回傳可以再領取的空位：上限減去執行中的數量。
// 暫停的類別沒有空位；結果只包含空位大於 0 的類別（含內部使用的 ResourceLocal）。
func (c *Control) Free(running map[string]int) map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	free := map[string]int{}
	if c.state.Paused {
		return free
	}
	for _, class := range append(slices.Clone(Classes), ResourceLocal) {
		if slices.Contains(c.state.PausedClasses, class) {
			continue
		}
		limit := c.limitLocked(class)
		if cp, ok := c.caps[class]; ok {
			limit = min(limit, cp)
		}
		if n := limit - running[class]; n > 0 {
			free[class] = n
		}
	}
	return free
}

// ClassStatus 是一個資源類別目前的設定。
type ClassStatus struct {
	Class  string
	Limit  int
	Paused bool
}

// Status 是控制設定的快照。
type Status struct {
	Paused  bool
	Classes []ClassStatus // 依 Classes 的順序
}

// Status 回傳目前的設定。
func (c *Control) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := Status{Paused: c.state.Paused}
	for _, class := range Classes {
		out.Classes = append(out.Classes, ClassStatus{
			Class: class, Limit: c.limitLocked(class), Paused: slices.Contains(c.state.PausedClasses, class),
		})
	}
	return out
}

// SetLimits 調整資源類別的並行上限，只改有給的類別。有任何一項不合法就全部不改。
func (c *Control) SetLimits(ctx context.Context, limits map[string]int) error {
	for class, n := range limits {
		if !slices.Contains(Classes, class) {
			return fmt.Errorf("%w %q，只有 %v", ErrUnknownClass, class, Classes)
		}
		if n < 1 || n > MaxLimit {
			return fmt.Errorf("%w（%s 是 %d）", ErrInvalidLimit, class, n)
		}
	}
	return c.update(ctx, func(st *controlState) {
		if st.Limits == nil {
			st.Limits = map[string]int{}
		}
		for class, n := range limits {
			st.Limits[class] = n
		}
	})
}

// Pause 暫停整個佇列（class 為空）或某個資源類別：執行中的任務會做完，但不會領取新任務（PIPE-R10）。
func (c *Control) Pause(ctx context.Context, class string) error {
	return c.setPaused(ctx, class, true)
}

// Resume 恢復整個佇列（class 為空）或某個資源類別。
// 恢復整個佇列不會解除個別資源類別的暫停，兩者各自獨立。
func (c *Control) Resume(ctx context.Context, class string) error {
	return c.setPaused(ctx, class, false)
}

func (c *Control) setPaused(ctx context.Context, class string, paused bool) error {
	if class != "" && !slices.Contains(Classes, class) {
		return fmt.Errorf("%w %q，只有 %v", ErrUnknownClass, class, Classes)
	}
	return c.update(ctx, func(st *controlState) {
		if class == "" {
			st.Paused = paused
			return
		}
		st.PausedClasses = slices.DeleteFunc(st.PausedClasses, func(s string) bool { return s == class })
		if paused {
			st.PausedClasses = append(st.PausedClasses, class)
			slices.Sort(st.PausedClasses)
		}
	})
}

// update 在複本上套用修改，先存進 settings 表，成功才換掉記憶體中的設定，然後喚醒 worker。
// 寫入失敗時記憶體與資料庫維持一致（都沒有改）。
func (c *Control) update(ctx context.Context, apply func(*controlState)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := controlState{Paused: c.state.Paused, PausedClasses: slices.Clone(c.state.PausedClasses)}
	if c.state.Limits != nil {
		next.Limits = make(map[string]int, len(c.state.Limits))
		for k, v := range c.state.Limits {
			next.Limits[k] = v
		}
	}
	apply(&next)
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := c.st.SetSetting(ctx, controlKey, string(raw)); err != nil {
		return fmt.Errorf("儲存佇列設定：%w", err)
	}
	c.state = next
	// 放寬上限或恢復之後可能有任務可以領取了。
	if c.wake != nil {
		c.wake()
	}
	return nil
}
