package prompts

import "strings"

// 行級 diff（PRM-R7）：比較兩個版本的內文。prompt 內文是使用者輸入，所以有大小上限，
// 超過上限時不做 LCS（O(n·m) 記憶體），改成整段刪除再整段新增。

// DiffOp 是 diff 一行的種類。
type DiffOp string

const (
	DiffEqual  DiffOp = "equal"
	DiffDelete DiffOp = "delete" // 只在 a 有
	DiffInsert DiffOp = "insert" // 只在 b 有
)

// DiffLine 是 diff 的一行（不含換行字元）。
type DiffLine struct {
	Op   DiffOp `json:"op"`
	Text string `json:"text"`
}

// maxDiffLines 是每一邊做 LCS 的行數上限；兩邊的乘積也受限制，避免表格太大。
const (
	maxDiffLines = 2000
	maxDiffCells = 1_000_000
)

// splitLines 以 LF 拆行（內文已經把 CRLF 換成 LF）；結尾的換行不產生多餘的空行。空字串沒有行。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Diff 回傳把 a 變成 b 的行級差異：依序列出每一行是共同的、被刪除的或新增的。
func Diff(a, b string) []DiffLine {
	x, y := splitLines(a), splitLines(b)
	// 去掉共同的開頭與結尾：prompt 的修改通常只在中間，LCS 只需要處理中間的部分。
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	out := make([]DiffLine, 0, len(x)+len(y))
	for _, l := range x[:pre] {
		out = append(out, DiffLine{DiffEqual, l})
	}
	out = append(out, diffMiddle(x[pre:len(x)-suf], y[pre:len(y)-suf])...)
	for _, l := range x[len(x)-suf:] {
		out = append(out, DiffLine{DiffEqual, l})
	}
	return out
}

func diffMiddle(x, y []string) []DiffLine {
	if len(x) == 0 || len(y) == 0 || len(x) > maxDiffLines || len(y) > maxDiffLines || len(x)*len(y) > maxDiffCells {
		out := make([]DiffLine, 0, len(x)+len(y))
		for _, l := range x {
			out = append(out, DiffLine{DiffDelete, l})
		}
		for _, l := range y {
			out = append(out, DiffLine{DiffInsert, l})
		}
		return out
	}
	// lcs[i][j]：x[i:] 與 y[j:] 的最長共同子序列長度。
	lcs := make([][]int32, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	out := make([]DiffLine, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		switch {
		case x[i] == y[j]:
			out = append(out, DiffLine{DiffEqual, x[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{DiffDelete, x[i]})
			i++
		default:
			out = append(out, DiffLine{DiffInsert, y[j]})
			j++
		}
	}
	for ; i < len(x); i++ {
		out = append(out, DiffLine{DiffDelete, x[i]})
	}
	for ; j < len(y); j++ {
		out = append(out, DiffLine{DiffInsert, y[j]})
	}
	return out
}
