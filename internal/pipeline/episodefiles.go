package pipeline

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// episodeFilePattern 是下載後的音檔檔名：episode.<副檔名>，或撞名時的 episode-<8 碼 16 進位>.<副檔名>（sha256 前 8 碼或隨機）；
// 媒體端點只開啟符合這個格式的檔名。
var episodeFilePattern = regexp.MustCompile(`^episode(-[0-9a-f]{8})?\.[a-z0-9]{1,5}$`)

// episodeFiles 管理媒體目錄裡 episode.* 音檔的所有權。同一集的 metadata 任務可能重疊（重跑、過期的舊任務、ASR 復原），
// 它們共用同一個目錄與檔名空間，所以所有建立、刪除與放回都集中在這裡，由一把鎖保護，並遵守下列規則：
//
//  1. 不蓋掉任何已存在的檔案：新檔只用 O_EXCL 建立（Stage），復原只在目的檔不存在時放回（Recover）。
//  2. 暫存的檔案屬於暫存它的那個執行，直到它 commit（Commit）或被丟棄（Discard）：期間清理（Sweep）與復原都不能動它，
//     所以檔名不會被別人重用，之後的 Discard 也只會刪到自己的檔案。
//  3. 只有檔案的擁有者能刪它（Discard）；沒有擁有者的 episode.* 檔（舊快照留下的、程序崩潰遺留的）由 Commit 之後的清理刪除，
//     快照現在指向的檔案（Commit 的 name）一定保留。
//  4. 檔名要等檔案真的刪掉之後才算釋放（刪除與解除登記在同一把鎖內）。
//
// 擁有者的記錄只存在記憶體：程序重啟之後沒有人擁有任何暫存檔，崩潰遺留的檔案由下一次 Commit 的清理刪除。
type episodeFiles struct {
	mu    sync.Mutex
	owned map[string]struct{} // 絕對路徑
}

// episodes 是程序內共用的實例（同一個資料目錄只有一個程序在使用）。
var episodes = &episodeFiles{owned: map[string]struct{}{}}

// key 是 owned 的鍵：媒體目錄的絕對路徑加檔名。
func (e *episodeFiles) key(dir, name string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, name)
}

// Stage 把下載完成的 part 放到 dir 並登記為這次執行擁有：依序用 O_EXCL 保留 episode<ext>、episode-<sha8><ext>，
// 都已存在時改用隨機的 episode-<8 碼><ext>（已存在就失敗），再 rename 蓋過自己剛建立的空檔。
// 回傳的檔案一定是這次建立的，不共用別的下載建立的檔案。O_EXCL 在任何檔案系統上都有效，不依賴 hard link。
// 建立與登記在同一把鎖之內，Sweep 不會看到還沒登記的新檔。保留與 rename 之間若程序崩潰會留下空檔，由之後的 Sweep 刪除。
func (e *episodeFiles) Stage(part, dir, ext, sum string) (string, error) {
	candidates := []string{"episode" + ext, "episode-" + sum[:8] + ext}
	for range 8 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		candidates = append(candidates, "episode-"+hex.EncodeToString(b[:])+ext)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, n := range candidates {
		dst := filepath.Join(dir, n)
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		switch {
		case err == nil:
			if err := f.Close(); err != nil {
				_ = os.Remove(dst)
				return "", err
			}
			if err := os.Rename(part, dst); err != nil {
				_ = os.Remove(dst)
				return "", err
			}
			e.owned[e.key(dir, n)] = struct{}{}
			return n, nil
		case errors.Is(err, fs.ErrExist):
			continue
		default:
			return "", err
		}
	}
	return "", errors.New("暫存音檔失敗：找不到可用的檔名")
}

// Commit 在擁有 name 的執行 commit 之後呼叫（name 也可以是沒有擁有者的沿用檔名）：檔案不再是暫存檔，
// 並刪除其他沒有擁有者的 episode.* 檔（舊快照指向的檔案、崩潰遺留）；其他執行已暫存的檔案保留。
func (e *episodeFiles) Commit(dir, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.owned, e.key(dir, name))
	e.sweepLocked(dir, name)
}

// Discard 在擁有 name 的執行沒有寫入結果時呼叫：刪除這個檔案並釋放檔名（同一把鎖內）。
func (e *episodeFiles) Discard(dir, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = os.Remove(filepath.Join(dir, name))
	delete(e.owned, e.key(dir, name))
}

// Sweep 刪除沒有擁有者的 episode.* 檔，keep 中的檔名保留（Commit 內部也用它）。
func (e *episodeFiles) Sweep(dir string, keep ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sweepLocked(dir, keep...)
}

func (e *episodeFiles) sweepLocked(dir string, keep ...string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if slices.Contains(keep, ent.Name()) || !episodeFilePattern.MatchString(ent.Name()) {
			continue
		}
		if _, owned := e.owned[e.key(dir, ent.Name())]; owned {
			continue
		}
		_ = os.Remove(filepath.Join(dir, ent.Name()))
	}
}

// Recover 把 ASR 重新下載的 part 放回快照記錄的檔名（那個檔案不見了）。這個檔名若被進行中的 metadata 任務擁有，
// 回傳可重試的錯誤（等它 commit 或丟棄）；檔案現在已經存在（例如下載期間別的任務 commit 了同名的新檔）則沿用現有的、
// 丟掉這次下載。兩種情況都不蓋掉任何檔案。檢查與 rename 在同一把鎖之內。
func (e *episodeFiles) Recover(part, dir, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, owned := e.owned[e.key(dir, name)]; owned {
		return &errclass.Error{Class: errclass.ClassRetryable, Source: "podcast", Message: "音檔正由另一個 metadata 任務更新，稍後重試。"}
	}
	dst := filepath.Join(dir, name)
	if _, err := os.Lstat(dst); err == nil {
		_ = os.Remove(part)
		return nil
	}
	return os.Rename(part, dst)
}
