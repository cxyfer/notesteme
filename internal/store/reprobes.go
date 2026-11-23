package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ReprobeKind 是重新 Probe 的排程工作（scheduled_jobs.kind，SRC-R18）；key 是影音 id。
const ReprobeKind = "source.reprobe"

// BetterSource 是重新 Probe 發現的更好來源（SRC-R23）。
type BetterSource struct {
	Source  string    `json:"source"`
	Lang    string    `json:"lang"`
	Kind    string    `json:"kind"`
	FoundAt time.Time `json:"found_at"`
	// EstimateUSD 為 nil 代表無法估算（SRC-R23）。
	EstimateUSD *float64 `json:"estimate_usd"`
}

// Reprobe 是一部影音重新 Probe 的狀態（source_reprobes）。下一次執行的時間只在 scheduled_jobs。
type Reprobe struct {
	ItemID string
	// Until 是發布時間＋7 天：超過之後不再執行（SRC-R18）。
	Until time.Time
	Runs  int
	// Sources 為 true 代表要重新列出平台字幕軌；SponsorBlock 為 true 代表要重新查詢 SponsorBlock（SRC-R20）。
	Sources      bool
	SponsorBlock bool
	// Better 為 nil 代表沒有發現更好的來源。
	Better *BetterSource
	// AutoReruns 是已經自動重跑的次數（SRC-R22：最多一次）；重跑 transcript 不會清除。
	AutoReruns int
}

// Reprobe 回傳影音的重新 Probe 狀態；沒有時回傳 ErrNotFound。
// better_json 損壞時視為沒有更好的來源（不讓影音詳情失敗）。
func (r *Repo) Reprobe(ctx context.Context, itemID string) (Reprobe, error) {
	row, err := r.q.GetReprobe(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return Reprobe{}, ErrNotFound
	}
	if err != nil {
		return Reprobe{}, err
	}
	out := Reprobe{
		ItemID: row.ItemID, Until: fromMs(row.UntilAt), Runs: int(row.Runs),
		Sources: row.Sources != 0, SponsorBlock: row.Sponsorblock != 0, AutoReruns: int(row.AutoReruns),
	}
	if row.BetterJson != nil {
		var b BetterSource
		if json.Unmarshal([]byte(*row.BetterJson), &b) == nil && b.Source != "" {
			b.FoundAt = b.FoundAt.UTC()
			out.Better = &b
		}
	}
	return out, nil
}

// SaveReprobe 寫入（或取代）影音的重新 Probe 狀態。
func (r *Repo) SaveReprobe(ctx context.Context, rp Reprobe) error {
	var better *string
	if rp.Better != nil {
		b, err := json.Marshal(rp.Better)
		if err != nil {
			return err
		}
		s := string(b)
		better = &s
	}
	return r.q.UpsertReprobe(ctx, sqlcdb.UpsertReprobeParams{
		ItemID: rp.ItemID, UntilAt: rp.Until.UnixMilli(), Runs: int64(rp.Runs),
		Sources: boolInt(rp.Sources), Sponsorblock: boolInt(rp.SponsorBlock),
		BetterJson: better, AutoReruns: int64(rp.AutoReruns), UpdatedAt: r.nowMs(),
	})
}

// SetStageMessage 只改步驟的訊息與動作，不改狀態與時間（SRC-R20、R21：完成的步驟帶提醒），並發出 stage.status 事件。
func (r *Repo) SetStageMessage(ctx context.Context, run StageRun, message string, actions []string) error {
	b, err := json.Marshal(nonNil(actions))
	if err != nil {
		return err
	}
	return r.inTx(ctx, func(tx *Repo) error {
		n, err := tx.q.SetStageMessage(ctx, sqlcdb.SetStageMessageParams{Message: message, ActionsJson: string(b), ID: run.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return tx.emitStage(ctx, run.ItemID, run.Stage, run.Status)
	})
}
