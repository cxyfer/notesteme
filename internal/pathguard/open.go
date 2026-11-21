package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrNotRegular 表示路徑存在，但不是一般檔案（目錄、FIFO、裝置等）。用 errors.Is 辨識。
var ErrNotRegular = errors.New("不是一般檔案")

// With 回傳多了 extra 這些根目錄的新 Guard（例如加上 <data>/uploads）；原 Guard 不變。
// extra 中空字串與相對路徑的處理同 New。Unrestricted 的 Guard 維持不受限制。
func (g *Guard) With(extra ...string) *Guard {
	if g == nil {
		return New(extra)
	}
	n := &Guard{unrestricted: g.unrestricted, roots: append([]string(nil), g.roots...)}
	for _, r := range extra {
		if filepath.IsAbs(r) {
			n.roots = append(n.roots, filepath.Clean(r))
		}
	}
	return n
}

// Resolve 回傳 path 解析 symlink 與 ".." 之後的絕對路徑；解析後不在根目錄之內時，
// 回傳可用 errors.Is(err, ErrOutsideRoots) 辨識的錯誤。路徑不存在時的語意同 Check。
// Unrestricted 的 Guard 只解析，不限制。
func (g *Guard) Resolve(path string) (string, error) {
	resolved, _, err := g.locate(path)
	return resolved, err
}

// locate 解析 path，並回傳包含它的（已解析的）根目錄；Unrestricted 時根目錄為空字串。
func (g *Guard) locate(path string) (resolved, root string, err error) {
	if g == nil {
		return "", "", ErrOutsideRoots
	}
	resolved, err = resolve(path)
	if err != nil {
		if g.unrestricted {
			return "", "", err
		}
		return "", "", fmt.Errorf("%w：%v", ErrOutsideRoots, err)
	}
	if g.unrestricted {
		return resolved, "", nil
	}
	for _, r := range g.roots {
		rr, err := resolve(r)
		if err != nil {
			continue // 同 Check：解析失敗的根目錄不放行任何路徑
		}
		if within(rr, resolved) {
			return resolved, rr, nil
		}
	}
	return "", "", ErrOutsideRoots
}

// Open 以唯讀開啟 path 並確認是一般檔案，回傳檔案與 fstat 的結果。
//
// 受限模式下對包含 path 的根目錄用 os.OpenRoot 開啟：解析之後、開啟之前路徑被換成指向根目錄外的 symlink，
// 也會被拒絕（ErrOutsideRoots）。以 O_NONBLOCK 開啟，FIFO 不會卡住；不是一般檔案回傳 ErrNotRegular。
// Unrestricted 的 Guard 用一般的 os.OpenFile 加同樣的一般檔案檢查。
func (g *Guard) Open(path string) (*os.File, os.FileInfo, error) {
	resolved, root, err := g.locate(path)
	if err != nil {
		return nil, nil, err
	}
	var f *os.File
	if g.unrestricted {
		f, err = os.OpenFile(resolved, openFlags, 0)
	} else {
		f, err = openUnder(root, resolved)
	}
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w：%s", ErrNotRegular, resolved)
	}
	return f, fi, nil
}

// openFlags：O_NONBLOCK 讓開啟 FIFO 時不會等到對端出現。
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK

// openUnder 在 root 底下開啟 resolved（root 已解析、resolved 在其內）。
func openUnder(root, resolved string) (*os.File, error) {
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return nil, ErrOutsideRoots
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.OpenFile(rel, openFlags, 0)
	if err != nil {
		// os.Root 擋下逃出根目錄的 symlink 時，錯誤沒有匯出的哨兵，只能比對訊息。
		if !errors.Is(err, fs.ErrNotExist) && strings.Contains(err.Error(), "escapes from parent") {
			return nil, fmt.Errorf("%w：%v", ErrOutsideRoots, err)
		}
		return nil, err
	}
	return f, nil
}
