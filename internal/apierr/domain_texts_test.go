package apierr

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// domainTextTarget 是會原樣放進 API 錯誤 message 的 domain 文字：型別（套件目錄:型別名稱）與欄位。
// validate.FieldError 的 Message 在任何套件都算（欄位錯誤的第一個說明是 422 的 message）。
// pipeline 的 errclass.Error 不在其中：那是步驟的狀態訊息（存進資料庫、UI 顯示），不是 API 的錯誤回應（A29）。
var domainTextTargets = map[string]string{
	"validate:FieldError":          "Message",
	"pipeline:InvalidError":        "Msg",
	"pipeline:TaskStateError":      "Msg",
	"pipeline:HubError":            "Msg",
	"pipeline:UpstreamError":       "Msg",
	"pipeline:PolicyError":         "Msg",
	"pipeline:BundleConflictError": "Message",
	"prompts:InvalidError":         "Msg",
	"assstyle:InvalidError":        "Msg",
	"subscription:URLError":        "Message",
	"subscription:PreviewError":    "Message",
	"subscription:ImportError":     "Message",
	"subscription:checkError":      "Message", // 預覽的錯誤（PreviewError）沿用它的訊息
	"bundle:Error":                 "Message",
	"cost:QueryError":              "Message",
	"notes:TemplateError":          "Message", // 筆記模板的錯誤（pipeline 轉成欄位錯誤）
	"llm:errclass.Error":           "Message", // 試跑 prompt 時 LLM 的錯誤（prompts_playground.go）
	"providers:errclass.Error":     "Message", // 測試 provider 連線（providers.go）
	"cost:errclass.Error":          "Message", // 預算擋下的試跑
	"subscription:errclass.Error":  "Message",
	// 新增影音、估價、預覽時外部工具的錯誤（pipeline 把它們的訊息原樣轉成 UpstreamError，items.go 的 up.Msg）
	"youtube:errclass.Error":  "Message",
	"bilibili:errclass.Error": "Message",
	"supadata:errclass.Error": "Message",
	"groq:errclass.Error":     "Message",
	"media:errclass.Error":    "Message",
}

// domainTextFuncs 是回傳訊息的函式（套件目錄:函式 → 回傳值的位置）：它們的 return 也照規則檢查，
// 其他套件直接使用它們的結果時視為已涵蓋。
var domainTextFuncs = map[string]int{
	"netguard:PolicyMessage": 0,
}

// domainTextExempt 是不必（或不能）在目錄中的 domain 文字：「檔案:運算式」→ 理由。
var domainTextExempt = map[string]string{
	// 轉述另一個已經在 domainTextTargets 中檢查過的錯誤（同一段文字，只是換一個容器）
	"pipeline/config_io.go:inv.Msg":       "匯入設定時把處理方案的 InvalidError（pipeline:InvalidError）轉成欄位錯誤",
	"pipeline/schema.go:invalid.Msg":      "prompt 引用的 prompts.InvalidError（prompts:InvalidError）轉成欄位錯誤",
	"pipeline/estimate.go:err.Error()":    "只在 errors.Is(err, errEstimateBudget) 時：errEstimateBudget 的固定句子（i18nSentinels 之外，見 TestEstimateBudgetMessageIsTranslated）",
	"pipeline/estimate.go:ce.Message":     "外部工具（youtube、bilibili、media…）的 errclass.Error，各套件的訊息已在 domainTextTargets 中檢查",
	"pipeline/ingest.go:ce.Message":       "同上：新增影音時外部工具的 errclass.Error",
	"pipeline/ingest_local.go:ce.Message": "同上：本機檔案的 ffprobe（media:errclass.Error）",
	"pipeline/render.go:ce.Message":       "同上：預覽時 ffmpeg（media:errclass.Error）",
	"subscription/preview.go:ce.Message":  "預覽沿用 checkError 的訊息（subscription:checkError）",
	"pipeline/note_api.go:te.Message":     "notes.TemplateError 的訊息（notes:TemplateError）",
	"pipeline/note_api.go:err.Error()":    "notes.ParseTemplate 只回傳 *TemplateError（notes:TemplateError），下一行再取它的 Message",
	// 第一個欄位的「路徑：說明」：API 不直接用這一句，改用範本 field_invalid（internal/api 的 fieldsError），說明另外翻譯
	"pipeline/errors.go:fs[0].Path + \"：\" + fs[0].Message":         "API 改用範本 field_invalid（路徑不翻譯、說明是欄位錯誤的 Message）",
	"prompts/prompts.go:fields[0].Path + \"：\" + fields[0].Message": "同上",
	// prompts.ParseVariables 的錯誤（fmt.Errorf）加上句號：兩種錯誤加句號後的格式在 domainFormats
	// Hub
	"hub/hubauth/hubauth.go:websession.ErrCSRF.Error()": "websession.ErrCSRF 的固定句子（與 Client 共用；internal/api 的 i18nSentinels 檢查它的翻譯）",
	"hub/hubrepo/share.go:e.Message":                    "bundle.Error 的訊息（bundle:Error 在 domainTextTargets 中檢查）",
	"prompts/prompts.go:err.Error() + \"。\"":            "ParseVariables 只回傳 vars.go 的兩種錯誤，加句號後的格式在 domainFormats（「有未關閉的「{{」…」「「{{%s}}」不是有效的變數寫法…」）",
}

// NFR-I18N-2：domain 套件中會原樣進入 API 錯誤 message 的文字都要在目錄中。掃描 internal/ 的正式程式碼，
// 找出 domainTextTargets 的型別的組合常值中訊息欄位的值：
//
//   - 字串常值、套件的字串常數 → 要在固定句子的目錄中；
//   - fmt.Sprintf("格式", …) → 格式要在 domainFormats；
//   - 所在函式的參數（helper，例如 add(path, msg string)）→ 改檢查同一套件中呼叫它的地方傳入的值；
//   - 其他（字串串接、函式呼叫）→ 要寫在 domainTextExempt 並說明理由。
func TestDomainTextsAreInCatalog(t *testing.T) {
	root := ".."
	fset := token.NewFileSet()
	type pkgFiles struct {
		dir   string
		files []*ast.File
	}
	pkgs := map[string]*pkgFiles{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			// API 的呼叫點另有檢查（internal/api 的 i18n 測試）；CLI、MCP、app 與 Hub 的 CLI、設定、儲存層的文字
			// 不是 API 的錯誤回應。Hub 的 API（hubapi、hubauth、hubrepo）在這裡檢查（apierr.New 的訊息）。
			switch rel {
			case "api", "apierr", "cli", "mcptools", "app", "store",
				"hub/hubmcp", "hub/hubcli", "hub/hubconfig", "hub/hubstore", "hub/hubblob", "hub/hubapi/cmd":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if pkgs[dir] == nil {
			pkgs[dir] = &pkgFiles{dir: dir}
		}
		pkgs[dir].files = append(pkgs[dir].files, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	usedExempt := map[string]bool{}
	domainCounts, hubCounts := map[string]int{}, map[string]int{}
	for _, pk := range pkgs {
		base := filepath.Base(pk.dir)
		counts := domainCounts
		if strings.HasPrefix(pk.dir, "hub") {
			counts = hubCounts
		}
		consts := map[string]string{}
		for _, f := range pk.files {
			collectConsts(f, consts)
		}
		// helper：函式名稱 → 參數位置（它的參數直接當成訊息）
		helpers := map[string][]int{}
		helperFormat := map[string]bool{} // helper 的參數是 fmt 的格式字串
		var values []domainValue
		for _, f := range pk.files {
			walkStack(f, func(n ast.Node, stack []ast.Node) {
				// Hub 直接以 apierr.New(status, code, 訊息) 建立錯誤；apierr.Newf 的範本要存在
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && len(call.Args) >= 3 {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "apierr" {
							switch sel.Sel.Name {
							case "New":
								values = append(values, domainValue{expr: call.Args[2], stack: append([]ast.Node(nil), stack...)})
							case "Newf":
								lit, ok := call.Args[2].(*ast.BasicLit)
								key := ""
								if ok {
									key, _ = strconv.Unquote(lit.Value)
								}
								counts["範本"]++
								if !HasTemplate(key) {
									pos := fset.Position(call.Pos())
									problems = append(problems, pk.dir+"/"+filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line)+" 範本不存在："+sourceText(fset, call.Args[2]))
								}
							}
						}
					}
					return
				}
				cl, ok := n.(*ast.CompositeLit)
				if !ok {
					return
				}
				typ := compositeType(cl, stack)
				field, ok := domainTextTargets[base+":"+typ]
				if !ok && (typ == "validate.FieldError" || base == "validate" && typ == "FieldError") {
					field, ok = "Message", true
				}
				if !ok || field == "" {
					return
				}
				for _, el := range cl.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == field {
						values = append(values, domainValue{expr: kv.Value, stack: append([]ast.Node(nil), stack...)})
					}
				}
			})
		}
		// visit 依規則檢查一個訊息的值；site 用來回報位置。
		var visit func(e ast.Expr, stack []ast.Node, depth int)
		var visitResult func(call *ast.CallExpr, idx int, stack []ast.Node) bool
		visited := map[string]bool{}
		visit = func(e ast.Expr, stack []ast.Node, depth int) {
			pos := fset.Position(e.Pos())
			site := pk.dir + "/" + filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
			key := pk.dir + "/" + filepath.Base(pos.Filename) + ":" + sourceText(fset, e)
			if _, ok := domainTextExempt[key]; ok {
				usedExempt[key] = true
				counts["例外"]++
				return
			}
			check := func(kind, s string) {
				if s == "" {
					return // 空字串代表沒有訊息（例如 (u, "") 的成功回傳）
				}
				counts[kind]++
				if en, ok := Translate(s, LangEn); !ok || en == s {
					problems = append(problems, site+" 不在目錄中："+s)
				}
			}
			switch x := e.(type) {
			case *ast.BasicLit:
				s, _ := strconv.Unquote(x.Value)
				check("常值", s)
				return
			case *ast.Ident:
				if s, ok := consts[x.Name]; ok {
					check("常數", s)
					return
				}
				if fn, idx := paramOf(stack, x.Name); fn != "" {
					helpers[fn] = appendUnique(helpers[fn], idx)
					counts["helper"]++
					return
				}
				// 區域變數：檢查所在函式中指定給它的每一個值
				if rhs := localAssignments(stack, x.Name); len(rhs) > 0 && depth < 3 {
					for _, r := range rhs {
						if r.resultOf != nil {
							visitResult(r.resultOf, r.idx, r.stack)
							continue
						}
						visit(r.expr, r.stack, depth+1)
					}
					return
				}
			case *ast.CallExpr:
				if !isSprintf(x) && visitResult(x, 0, stack) {
					return
				}
				if isSprintf(x) {
					switch f := x.Args[0].(type) {
					case *ast.BasicLit:
						s, _ := strconv.Unquote(f.Value)
						counts["格式"]++
						if !hasFormat(s) {
							problems = append(problems, site+" 格式不在 domainFormats："+s)
						}
						return
					case *ast.Ident:
						if s, ok := consts[f.Name]; ok {
							counts["格式"]++
							if !hasFormat(s) {
								problems = append(problems, site+" 格式不在 domainFormats："+s)
							}
							return
						}
						if fn, idx := paramOf(stack, f.Name); fn != "" {
							helpers[fn] = appendUnique(helpers[fn], idx)
							helperFormat[fn] = true
							counts["helper"]++
							return
						}
					}
				}
			}
			problems = append(problems, site+" 無法歸類（寫進 domainTextExempt 並說明理由，或改成常值／格式）："+key)
		}
		for key, idx := range domainTextFuncs {
			dir, fn, _ := strings.Cut(key, ":")
			if dir != pk.dir {
				continue
			}
			found := false
			for _, f := range pk.files {
				for _, r := range funcReturns(f, fn, idx) {
					found = true
					values = append(values, r)
				}
			}
			if !found {
				problems = append(problems, "domainTextFuncs 的 "+key+" 找不到")
			}
		}
		// visitResult 檢查函式呼叫的結果：同一套件的函式檢查它的 return，domainTextFuncs 的函式視為已涵蓋。
		visitResult = func(call *ast.CallExpr, idx int, stack []ast.Node) bool {
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok {
					for key, i := range domainTextFuncs {
						dir, name, _ := strings.Cut(key, ":")
						if filepath.Base(dir) == x.Name && name == fn.Sel.Name && i == idx {
							counts["函式"]++
							return true
						}
					}
				}
			case *ast.Ident:
				if visited[fn.Name] {
					return true
				}
				var rs []domainValue
				for _, f := range pk.files {
					rs = append(rs, funcReturns(f, fn.Name, idx)...)
				}
				if len(rs) == 0 {
					return false
				}
				visited[fn.Name] = true
				counts["函式"]++
				for _, r := range rs {
					visit(r.expr, r.stack, 1)
				}
				return true
			}
			return false
		}
		for _, v := range values {
			if v.resultOf != nil {
				visitResult(v.resultOf, v.idx, v.stack)
				continue
			}
			visit(v.expr, v.stack, 0)
		}
		// helper 的呼叫端：同一套件中呼叫它的地方，對應位置的參數也照上面的規則檢查（helper 再轉手給 helper 時
		// 外層的 helper 也記下來，重複到沒有新的 helper 為止）。
		for done := map[string]bool{}; ; {
			var todo []string
			for name := range helpers {
				if !done[name] {
					todo = append(todo, name)
				}
			}
			if len(todo) == 0 {
				break
			}
			for _, name := range todo {
				done[name] = true
				for _, f := range pk.files {
					walkStack(f, func(n ast.Node, stack []ast.Node) {
						call, ok := n.(*ast.CallExpr)
						if !ok {
							return
						}
						cn := ""
						switch fn := call.Fun.(type) {
						case *ast.Ident:
							cn = fn.Name
						case *ast.SelectorExpr:
							cn = fn.Sel.Name
						}
						if cn != name {
							return
						}
						for _, idx := range helpers[name] {
							if idx >= len(call.Args) {
								continue
							}
							a := call.Args[idx]
							if helperFormat[name] {
								// 參數是格式字串：常值要在 domainFormats
								if lit, ok := a.(*ast.BasicLit); ok {
									s, _ := strconv.Unquote(lit.Value)
									counts["格式"]++
									if !hasFormat(s) && !inCatalog(s) {
										pos := fset.Position(a.Pos())
										problems = append(problems, pk.dir+"/"+filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line)+" 格式不在 domainFormats："+s)
									}
									continue
								}
							}
							visit(a, stack, 0)
						}
					})
				}
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
	for k := range domainTextExempt {
		if !usedExempt[k] {
			t.Errorf("domainTextExempt 的 %s 已經沒有用到，請移除", k)
		}
	}
	for _, c := range []struct {
		name   string
		counts map[string]int
	}{{"domain 文字", domainCounts}, {"Hub API 的錯誤訊息", hubCounts}} {
		t.Logf("%s：常值 %d、常數 %d、格式 %d、範本 %d、helper %d、回傳訊息的函式 %d、例外 %d", c.name,
			c.counts["常值"], c.counts["常數"], c.counts["格式"], c.counts["範本"], c.counts["helper"], c.counts["函式"], c.counts["例外"])
	}
	t.Logf("未走目錄 %d", len(problems))
}

// inCatalog 回報 s 是不是目錄中的固定句子（沒有參數的格式字串會被當成固定句子）。
func inCatalog(s string) bool { _, ok := lookupMessage(s); return ok }

// localAssignments 找出所在函式中指定給區域變數 name 的值（name := …、name = …、var name = …）。
func localAssignments(stack []ast.Node, name string) []domainValue {
	for i := len(stack) - 1; i >= 0; i-- {
		var body *ast.BlockStmt
		switch f := stack[i].(type) {
		case *ast.FuncDecl:
			body = f.Body
		case *ast.FuncLit:
			body = f.Body
		default:
			continue
		}
		var out []domainValue
		walkStack(body, func(n ast.Node, st []ast.Node) {
			full := append(append([]ast.Node{}, stack[:i+1]...), st...)
			switch a := n.(type) {
			case *ast.AssignStmt:
				if len(a.Rhs) == 1 && len(a.Lhs) > 1 {
					if call, ok := a.Rhs[0].(*ast.CallExpr); ok {
						for j, l := range a.Lhs {
							if id, ok := l.(*ast.Ident); ok && id.Name == name {
								out = append(out, domainValue{resultOf: call, idx: j, stack: full})
							}
						}
					}
					return
				}
				if len(a.Lhs) != len(a.Rhs) {
					return
				}
				for j, l := range a.Lhs {
					if id, ok := l.(*ast.Ident); ok && id.Name == name {
						out = append(out, domainValue{expr: a.Rhs[j], stack: full})
					}
				}
			case *ast.ValueSpec:
				for j, id := range a.Names {
					if id.Name == name && j < len(a.Values) {
						out = append(out, domainValue{expr: a.Values[j], stack: full})
					}
				}
			}
		})
		if len(out) > 0 {
			return out
		}
		// closure 用到外層函式的區域變數：往外找
	}
	return nil
}

type domainValue struct {
	expr  ast.Expr
	stack []ast.Node
	// resultOf 不是 nil 時，值是這個呼叫的第 idx 個結果（u, msg := f(…)）。
	resultOf *ast.CallExpr
	idx      int
}

// funcReturns 是檔案中函式 name 的 return 的第 idx 個值（不含其中 closure 的 return）。
func funcReturns(f *ast.File, name string, idx int) []domainValue {
	var out []domainValue
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		walkStack(fd.Body, func(n ast.Node, st []ast.Node) {
			r, ok := n.(*ast.ReturnStmt)
			if !ok || idx >= len(r.Results) {
				return
			}
			for _, a := range st {
				if _, lit := a.(*ast.FuncLit); lit {
					return
				}
			}
			out = append(out, domainValue{expr: r.Results[idx], stack: append([]ast.Node{fd}, st...)})
		})
	}
	return out
}

func appendUnique(s []int, v int) []int {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func hasFormat(f string) bool {
	for _, c := range compiledFormats {
		if c.hant == f {
			return true
		}
	}
	return false
}

func isSprintf(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || len(c.Args) == 0 {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "fmt" && sel.Sel.Name == "Sprintf"
}

// compositeType 是組合常值的型別名稱；省略型別的元素（[]validate.FieldError{{…}}）取外層的元素型別。
func compositeType(cl *ast.CompositeLit, stack []ast.Node) string {
	if cl.Type != nil {
		return exprName(cl.Type)
	}
	for i := len(stack) - 1; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.KeyValueExpr:
			continue
		case *ast.CompositeLit:
			switch t := p.Type.(type) {
			case *ast.ArrayType:
				return strings.TrimPrefix(exprName(t.Elt), "*")
			case *ast.MapType:
				return strings.TrimPrefix(exprName(t.Value), "*")
			}
			return ""
		default:
			return ""
		}
	}
	return ""
}

func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprName(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprName(t.X)
	}
	return ""
}

// paramOf 回報 name 是不是所在函式的參數：回傳函式名稱與參數位置。
// 區域的 closure（add := func(path, msg string) {…}）以變數名稱當成函式名稱。
func paramOf(stack []ast.Node, name string) (string, int) {
	for i := len(stack) - 1; i >= 0; i-- {
		var fname string
		var ft *ast.FuncType
		switch f := stack[i].(type) {
		case *ast.FuncDecl:
			fname, ft = f.Name.Name, f.Type
		case *ast.FuncLit:
			ft = f.Type
			if i > 0 {
				if as, ok := stack[i-1].(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
					if id, ok := as.Lhs[0].(*ast.Ident); ok {
						fname = id.Name
					}
				}
			}
		default:
			continue
		}
		idx := 0
		for _, p := range ft.Params.List {
			if len(p.Names) == 0 {
				idx++
				continue
			}
			for _, n := range p.Names {
				if n.Name == name {
					return fname, idx
				}
				idx++
			}
		}
		if _, isLit := stack[i].(*ast.FuncLit); !isLit {
			return "", 0
		}
	}
	return "", 0
}

// collectConsts 收集套件層級的字串常數與只指定一次的字串變數（const／var x = "…"）。
func collectConsts(f *ast.File, out map[string]string) {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
			continue
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						out[n.Name], _ = strconv.Unquote(lit.Value)
					}
				}
			}
		}
	}
}

func walkStack(root ast.Node, fn func(n ast.Node, stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		fn(n, stack)
		stack = append(stack, n)
		return true
	})
}

func sourceText(fset *token.FileSet, e ast.Expr) string {
	start, end := fset.Position(e.Pos()), fset.Position(e.End())
	b, err := os.ReadFile(start.Filename)
	if err != nil || end.Offset > len(b) {
		return "?"
	}
	return string(b[start.Offset:end.Offset])
}
