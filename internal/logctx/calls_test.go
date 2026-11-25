package logctx

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// noContextAllowed 是可以不傳 context 的 log 呼叫（「檔案:函式」→ 理由）。這些 log 仍然有 request_id：
// handler 在 context 中沒有 request ID 時補上 boot ID（BootID）。新增項目前先確認呼叫的地方真的拿不到 context。
var noContextAllowed = map[string]string{
	"internal/app/socket.go:(*peerListener).Accept": "net.Listener 的 Accept 沒有 context；拒絕本機 socket 的連線發生在任何請求之前，屬於程序層級（O26）",
}

// NFR-OBS-1（ci）：log 呼叫必須用 *Context 版本（InfoContext、WarnContext…）並傳入 context，handler 才補得上
// request_id、task、item、stage。這裡用型別資訊掃描 internal/ 與 cmd/ 的正式程式碼（不含 _test.go 與 features/），
// 找出 slog 的 Debug/Info/Warn/Error 套件函式，以及簽章是 (string, ...any) 的同名方法（*slog.Logger 或只宣告這幾個方法的介面）。
// 之後新增不帶 context 的 log 會讓這個測試失敗；真的拿不到 context 時加進 noContextAllowed 並寫明理由。
func TestLogCallsPassContext(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("沒有 go 指令，無法取得型別資訊")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pkgs := goList(t, root)
	exports := map[string]string{}
	for _, p := range pkgs {
		if p.Export != "" {
			exports[p.ImportPath] = p.Export
		}
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok {
			return nil, errors.New("沒有 export data：" + path)
		}
		return os.Open(f)
	})

	var found []string
	used := map[string]bool{}
	checked := 0
	for _, p := range pkgs {
		if p.DepOnly || p.Standard || !strings.HasPrefix(p.ImportPath, "github.com/cxyfer/notesteme/") {
			continue
		}
		rel, _ := filepath.Rel(root, p.Dir)
		rel = filepath.ToSlash(rel)
		if rel == "features" || strings.HasPrefix(rel, "features/") {
			continue
		}
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
		conf := types.Config{Importer: imp}
		if _, err := conf.Check(p.ImportPath, fset, files, info); err != nil {
			t.Fatalf("型別檢查 %s：%v", p.ImportPath, err)
		}
		checked++
		for _, f := range files {
			for _, decl := range f.Decls {
				fn := declName(decl)
				ast.Inspect(decl, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !isNoContextLog(info.Uses[sel.Sel]) {
						return true
					}
					pos := fset.Position(call.Pos())
					file, _ := filepath.Rel(root, pos.Filename)
					key := filepath.ToSlash(file) + ":" + fn
					if _, ok := noContextAllowed[key]; ok {
						used[key] = true
						return true
					}
					found = append(found, key+"（第 "+itoa(pos.Line)+" 行）")
					return true
				})
			}
		}
	}
	if checked < 20 {
		t.Fatalf("只檢查了 %d 個套件，go list 的結果不完整", checked)
	}
	sort.Strings(found)
	for _, s := range found {
		t.Errorf("log 沒有傳 context（改用 InfoContext/WarnContext/ErrorContext/DebugContext 並傳入 ctx）：%s", s)
	}
	for k := range noContextAllowed {
		if !used[k] {
			t.Errorf("noContextAllowed 的 %s 已經沒有不帶 context 的 log，請從清單移除", k)
		}
	}
	t.Logf("檢查了 %d 個套件；允許不帶 context 的 log %d 處", checked, len(noContextAllowed))
}

// isNoContextLog 回報 obj 是不是不帶 context 的 slog 呼叫。
func isNoContextLog(obj types.Object) bool {
	fn, ok := obj.(*types.Func)
	if !ok {
		return false
	}
	switch fn.Name() {
	case "Debug", "Info", "Warn", "Error":
	default:
		return false
	}
	sig := fn.Type().(*types.Signature)
	if sig.Recv() == nil {
		return fn.Pkg() != nil && fn.Pkg().Path() == "log/slog"
	}
	// 方法：簽章與 *slog.Logger 的 Info 相同，(msg string, args ...any)，沒有回傳值。
	ps := sig.Params()
	if !sig.Variadic() || ps.Len() != 2 || sig.Results().Len() != 0 {
		return false
	}
	if b, ok := ps.At(0).Type().(*types.Basic); !ok || b.Kind() != types.String {
		return false
	}
	s, ok := ps.At(1).Type().(*types.Slice)
	if !ok {
		return false
	}
	it, ok := s.Elem().Underlying().(*types.Interface)
	return ok && it.Empty()
}

func declName(d ast.Decl) string {
	fd, ok := d.(*ast.FuncDecl)
	if !ok {
		return "(package)"
	}
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	var b strings.Builder
	switch r := fd.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		b.WriteString("(*" + identName(r.X) + ")")
	default:
		b.WriteString(identName(r))
	}
	return b.String() + "." + fd.Name.Name
}

func identName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return identName(x.X)
	case *ast.IndexListExpr:
		return identName(x.X)
	}
	return "?"
}

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Standard   bool
	DepOnly    bool
}

// goList 列出 internal/ 與 cmd/ 的套件及所有依賴，並取得編譯後的 export data（型別資訊）。
func goList(t *testing.T, root string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,Standard,DepOnly", "./internal/...", "./cmd/...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list：%v\n%s", err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}
