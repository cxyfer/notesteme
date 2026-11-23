// Package sqlitedb 是 Client（internal/store）與 Hub（internal/hub/hubstore/sqlite）共用的 SQLite 連線設定：
// modernc.org/sqlite、WAL、foreign keys，DB 與 WAL／SHM 一律 0600（NFR-SEC-5）。
// 只依賴標準函式庫與 driver，Hub 才不會間接帶進 Client 的 domain 套件（ADR-0030 第 1 點）。
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"

	_ "modernc.org/sqlite" // 註冊 "sqlite" driver
)

// EnsurePrivateFiles 讓 DB 與 WAL／SHM 的權限為 0600；create 為 true 時先以 0600 建立 DB 檔。
// SQLite 建立 WAL／SHM 時沿用 DB 檔的權限，所以要在開啟之前建好 DB，開啟後再確認一次。
func EnsurePrivateFiles(path string, create bool) error {
	if create {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return fmt.Errorf("建立資料庫檔失敗：%w", err)
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("設定 %s 的權限失敗：%w", p, err)
		}
	}
	return nil
}

// Open 開啟 path（必須是絕對路徑）的連線池；readOnly 時加上 query_only。
func Open(path string, readOnly bool) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("開啟資料庫失敗：%w", err)
	}
	return db, nil
}

// CheckWritable 確認 db 可以取得寫入鎖（/readyz 使用）；不會寫入任何資料。
func CheckWritable(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "ROLLBACK")
	return err
}
