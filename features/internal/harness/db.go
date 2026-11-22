package harness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // 註冊 "sqlite" driver（與 production 相同）
)

// 場景明確讓 server 停下之後（例如 graceful shutdown），讀取狀態的步驟不能隱含啟動新的 server：
// 新 server 啟動時的 RecoverStale（PIPE-R11）會把租約放回，蓋掉剛才那次關閉的結果。
// 這裡以唯讀方式直接開 SQLite 查詢，不經過任何 production 程式。

// dbQuery 以唯讀連線開啟場景的資料庫並執行 fn。資料庫不存在時回傳錯誤，不會建立它。
func (w *World) dbQuery(fn func(ctx context.Context, db *sql.DB) error) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(w.DataDir(), "notesteme.db"), RawQuery: url.Values{
		"mode":    {"ro"},
		"_pragma": {"busy_timeout(5000)"},
	}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("開啟資料庫：%w", err)
	}
	defer db.Close()
	// 只是防止卡死的保險。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return fn(ctx, db)
}

// DBStageStatus 直接讀取資料庫中影音某個步驟最近一次執行的狀態；沒有該步驟時 found 為 false。
func (w *World) DBStageStatus(itemID, stage string) (status string, found bool, err error) {
	err = w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		e := db.QueryRowContext(ctx,
			`SELECT status FROM stage_runs WHERE item_id = ? AND stage = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
			itemID, stage).Scan(&status)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		found = e == nil
		return e
	})
	return status, found, err
}

// DBLeasedTasks 直接讀取資料庫中被領取（leased）的任務數，依資源類別分組。
func (w *World) DBLeasedTasks() (map[string]int, error) {
	out := map[string]int{}
	err := w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `SELECT resource_class, COUNT(*) FROM tasks WHERE state = 'leased' GROUP BY resource_class`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var class string
			var n int
			if err := rows.Scan(&class, &n); err != nil {
				return err
			}
			out[class] = n
		}
		return rows.Err()
	})
	return out, err
}

// DBAgentHolds 回報 Agent 任務 taskID 是否仍以 leaseToken 被 owner 持有（AGT：`the agent … holds … tasks`）。
// 唯讀地直接查 SQLite：Agent 的持有狀態沒有對應的 API。
func (w *World) DBAgentHolds(taskID, leaseToken, owner string) (bool, error) {
	var n int
	err := w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks WHERE id = ? AND executor = 'agent' AND state = 'leased' AND lease_token = ? AND lease_owner = ?`,
			taskID, leaseToken, owner).Scan(&n)
	})
	return n > 0, err
}
