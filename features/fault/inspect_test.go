//go:build faultinject && unix

package fault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cxyfer/notesteme/internal/sqlitedb"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// outcome 是一次執行最後的狀態，用來和沒有中斷的基準比較。
// 不比較產出檔的 bytes 或 sha256：產出的 ID 是 UUIDv7，來源紀錄的 parents 也帶著這些 ID，每次執行必然不同；
// 改比步驟狀態、現行產出的種類，以及解碼後字幕句的時間與文字。
type outcome struct {
	items     int
	stages    map[string]string // stage[lang] → status/block_reason
	artifacts map[string]string // kind/variant/lang → 內容摘要（字幕產出是字幕句的雜湊；其他格式只比對存在）
	openTasks int               // pending 或 leased 的任務
	// orphanTmp 是 artifacts/ 下殘留的暫存檔（.tmp-*）；orphanFiles 是沒有任何 DB 記錄指向的正式產出檔。
	orphanTmp, orphanFiles []string
}

// complete 回報 pipeline 是否做完：只有一部影音、沒有未結束的任務、每個步驟都是 done 或 skipped。
func (o *outcome) complete() error {
	if o.items != 1 {
		return fmt.Errorf("媒體庫有 %d 部影音，應該是 1 部", o.items)
	}
	if o.openTasks != 0 {
		return fmt.Errorf("還有 %d 個沒有結束的任務（卡住）", o.openTasks)
	}
	for k, v := range o.stages {
		if !strings.HasPrefix(v, store.StatusDone+"/") && !strings.HasPrefix(v, store.StatusSkipped+"/") {
			return fmt.Errorf("步驟 %s 是 %s", k, v)
		}
	}
	if len(o.orphanTmp) > 0 {
		return fmt.Errorf("重新啟動後仍有殘留的暫存檔：%v", o.orphanTmp)
	}
	return nil
}

// diff 回傳與 got 的差異（空字串代表相同）。
func (o *outcome) diff(got *outcome) string {
	var b strings.Builder
	cmpMap := func(what string, want, have map[string]string) {
		keys := map[string]bool{}
		for k := range want {
			keys[k] = true
		}
		for k := range have {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			if want[k] != have[k] {
				fmt.Fprintf(&b, "  %s %s：基準 %q，實際 %q\n", what, k, want[k], have[k])
			}
		}
	}
	cmpMap("步驟", o.stages, got.stages)
	cmpMap("產出", o.artifacts, got.artifacts)
	return b.String()
}

func dbPath(root string) string { return filepath.Join(root, "data", store.DBFileName) }

// integrity 對 path 執行 PRAGMA integrity_check 與 foreign_key_check。
func integrity(path string) error {
	db, err := sqlitedb.Open(path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil {
		return fmt.Errorf("integrity_check：%w", err)
	}
	if res != "ok" {
		return fmt.Errorf("integrity_check 回報 %q", res)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check：%w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("foreign_key_check 找到違反外鍵的資料列")
	}
	return rows.Err()
}

// integrityOfCopy 把被殺當下的 DB、-wal、-shm 複製到暫存目錄再檢查：直接開啟原檔會先做 WAL recovery，
// 關閉時還會 checkpoint，等於替重新啟動的程序先做了一部分事。DB 還沒建立（在 migration 之前被殺）時不檢查。
func integrityOfCopy(root string) error {
	src := dbPath(root)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	dir, err := os.MkdirTemp("", "nstfaultdb")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dst := filepath.Join(dir, store.DBFileName)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(src+suffix, dst+suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return integrity(dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// inspect 讀出 root 的最後狀態，並檢查 DB 的完整性（NFR-REL-1）與每份已記錄產出的 sha256（NFR-REL-2）。
func inspect(root string) (*outcome, error) { return inspectExcept(root, "") }

// inspectExcept 同 inspect，但不檢查 ID 為 skip 的產出（損毀測試刻意改壞、已被標記的那一份）。
func inspectExcept(root, skip string) (*outcome, error) {
	path := dbPath(root)
	if err := integrity(path); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(path, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx := context.Background()
	o := &outcome{stages: map[string]string{}, artifacts: map[string]string{}}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM items WHERE deleted_at IS NULL").Scan(&o.items); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE state IN ('pending', 'leased')").Scan(&o.openTasks); err != nil {
		return nil, err
	}
	// 每個（步驟、語言）最新的一筆 stage run。
	rows, err := db.QueryContext(ctx, `
		SELECT stage, lang, status, block_reason FROM stage_runs s
		WHERE NOT EXISTS (SELECT 1 FROM stage_runs n WHERE n.item_id = s.item_id AND n.stage = s.stage AND n.lang = s.lang
		                  AND (n.created_at > s.created_at OR (n.created_at = s.created_at AND n.id > s.id)))`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var stage, lang, status, reason string
		if err := rows.Scan(&stage, &lang, &status, &reason); err != nil {
			rows.Close()
			return nil, err
		}
		o.stages[stage+"["+lang+"]"] = status + "/" + reason
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	referenced := map[string]bool{}
	rows, err = db.QueryContext(ctx, "SELECT id, kind, variant, lang, format, path, sha256, size, superseded_by IS NULL, corrupt_at IS NOT NULL FROM artifacts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, variant, lang, format, rel, sha string
		var size int64
		var current, corrupt bool
		if err := rows.Scan(&id, &kind, &variant, &lang, &format, &rel, &sha, &size, &current, &corrupt); err != nil {
			return nil, err
		}
		referenced[filepath.FromSlash(rel)] = true
		if id == skip {
			if current {
				return nil, fmt.Errorf("損毀的產出 %s 仍是現行版本", id)
			}
			continue
		}
		if corrupt {
			return nil, fmt.Errorf("產出 %s 被標記為損毀", rel)
		}
		b, err := os.ReadFile(filepath.Join(root, "data", filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("DB 記錄的產出檔讀不到（DB 先於檔案記錄，違反 NFR-REL-2）：%w", err)
		}
		if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != sha || int64(len(b)) != size {
			return nil, fmt.Errorf("產出檔 %s 的 sha256 或大小與 DB 記錄不符（NFR-REL-2）", rel)
		}
		if !current {
			continue
		}
		key := kind + "/" + variant + "/" + lang
		if _, dup := o.artifacts[key]; dup {
			return nil, fmt.Errorf("同一組 %s 有兩份現行產出", key)
		}
		o.artifacts[key] = contentDigest(format, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dirs, err := os.ReadDir(filepath.Join(root, "data", "artifacts"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, "data", "artifacts", d.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			rel := filepath.Join("artifacts", d.Name(), f.Name())
			switch {
			case strings.HasPrefix(f.Name(), ".tmp-"):
				o.orphanTmp = append(o.orphanTmp, rel)
			case !referenced[rel] && !f.IsDir():
				o.orphanFiles = append(o.orphanFiles, rel)
			}
		}
	}
	return o, nil
}

// contentDigest 是產出內容中與執行無關的部分：字幕產出取字幕句的時間與文字（不含來源紀錄、ID）；
// 其他格式只記格式名稱。
func contentDigest(format string, b []byte) string {
	if format != "notesteme-cues+json" {
		return format
	}
	tr, err := subtitle.DecodeCuesJSON(bytes.NewReader(b))
	if err != nil {
		return "無法解碼：" + err.Error()
	}
	type cue struct {
		S, E int64
		T    string
	}
	cues := make([]cue, len(tr.Cues))
	for i, c := range tr.Cues {
		cues[i] = cue{c.StartMS, c.EndMS, c.Text}
	}
	j, _ := json.Marshal(struct {
		Lang, Variant string
		Cues          []cue
	}{tr.Lang, string(tr.Variant), cues})
	sum := sha256.Sum256(j)
	return fmt.Sprintf("%s cues=%d %s", format, len(cues), hex.EncodeToString(sum[:8]))
}
