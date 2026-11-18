// Package pathguard 限制 API 與 MCP 可以新增的本機路徑（GRD-R7、GRD-R8）。
//
// 比對前先解析 symlink 與 ".."，再用 filepath.Rel 判斷路徑是否在根目錄之內；
// 不用字串前綴，所以 /media2 不會被當成在 /media 之內。
package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// ErrOutsideRoots 表示路徑不在設定的根目錄之內（含無法確認的情況，一律拒絕）。用 errors.Is 辨識。
var ErrOutsideRoots = errors.New("路徑不在允許的根目錄之內")

// Guard 檢查路徑是否在根目錄之內。零值與 nil 都拒絕所有路徑（fail closed）。
type Guard struct {
	roots        []string
	unrestricted bool
}

// New 建立只接受 roots 底下路徑的 Guard。
// 空字串與相對路徑的根目錄會被忽略：它們會依程序的工作目錄解讀，不能當成防護的依據。
func New(roots []string) *Guard {
	g := &Guard{}
	for _, r := range roots {
		if filepath.IsAbs(r) {
			g.roots = append(g.roots, filepath.Clean(r))
		}
	}
	return g
}

// Unrestricted 建立不限制路徑的 Guard（獨立模式的 CLI，GRD-R8）。
func Unrestricted() *Guard { return &Guard{unrestricted: true} }

// Check 回傳 nil 代表 path 解析後位於某個根目錄之內；否則回傳可用 errors.Is(err, ErrOutsideRoots) 辨識的錯誤。
//
// path 必須是絕對路徑。檔案不存在時，解析最長的既有前綴，再接上其餘的部分，
// 所以尚未建立的檔案也能判斷，而且經過 symlink 的前綴一樣會被解析。
// 呼叫端之後存取檔案時應使用同一個路徑；檔案在檢查與使用之間被換成 symlink 的競態不在這裡處理（ING-05）。
func (g *Guard) Check(path string) error {
	if g == nil {
		return ErrOutsideRoots
	}
	if g.unrestricted {
		return nil
	}
	resolved, err := resolve(path)
	if err != nil {
		return fmt.Errorf("%w：%v", ErrOutsideRoots, err)
	}
	for _, root := range g.roots {
		// 根目錄本身也要解析 symlink。尚未建立的根目錄（例如還沒放過檔案的 inbox）用同樣的前綴解析；
		// 解析失敗（無權限、symlink 迴圈）的根目錄略過，不放行任何路徑。
		r, err := resolve(root)
		if err != nil {
			continue
		}
		if within(r, resolved) {
			return nil
		}
	}
	return ErrOutsideRoots
}

// within 回報 path 是否等於 root 或在 root 之下；兩者都已解析。
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolve 回傳 p 解析 symlink 與 ".." 之後的絕對路徑。
//
// 不先對 p 做 filepath.Clean：a/link/../b 在作業系統中是「先跟著 link 再往上」，
// 與純字串的 Clean 結果不同。EvalSymlinks 依作業系統的語意處理既有的部分；
// 不存在的部分不可能含有 symlink，才用字串方式化簡。
func resolve(p string) (string, error) { return resolveDepth(p, 0) }

// maxSymlinkDepth 是解析 dangling symlink 時最多跟隨的次數，防止迴圈；超過一律拒絕。
const maxSymlinkDepth = 40

// resolveDepth 是 resolve 的實作；depth 記錄已跟隨的 dangling symlink 次數。
func resolveDepth(p string, depth int) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("必須是絕對路徑")
	}
	if depth > maxSymlinkDepth {
		return "", errors.New("symlink 層數過多")
	}
	sep := string(filepath.Separator)
	cur := p
	var tail []string // 從尾端依序切下來的元件
	join := func(r string) string {
		slices.Reverse(tail)
		return filepath.Clean(filepath.Join(append([]string{r}, tail...)...))
	}
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return join(r), nil
		}
		// 元件不存在，或前面的元件是檔案（ENOTDIR）：切掉最後一段再試。其他錯誤（權限、symlink 迴圈）拒絕。
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		// dangling symlink：EvalSymlinks 因目標不存在而失敗，但這個元件本身是 symlink，
		// 不能當成不存在的元件切掉，必須解析它指向哪裡，再接上其餘的部分。
		if fi, lerr := os.Lstat(cur); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(cur)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				// 相對目標從 symlink 所在目錄起算。父目錄先解析（不能用會 Clean 的 filepath.Dir），
				// 目標字串原樣接上，由遞迴依作業系統語意處理其中的 ".."。
				i := strings.LastIndex(cur, sep)
				parent := cur[:i]
				if parent == "" {
					parent = sep
				}
				rp, err := resolveDepth(parent, depth+1)
				if err != nil {
					return "", err
				}
				target = strings.TrimRight(rp, sep) + sep + target
			}
			rt, err := resolveDepth(target, depth+1)
			if err != nil {
				return "", err
			}
			return join(rt), nil
		}
		trimmed := strings.TrimRight(cur, sep)
		i := strings.LastIndex(trimmed, sep)
		if i < 0 || trimmed == "" {
			return "", os.ErrNotExist
		}
		tail = append(tail, trimmed[i+1:])
		cur = trimmed[:i+1]
		if cur != sep {
			cur = strings.TrimRight(cur, sep)
		}
		if cur == "" {
			cur = sep
		}
	}
}
