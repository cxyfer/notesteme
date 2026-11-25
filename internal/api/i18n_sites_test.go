package api

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

	"github.com/cxyfer/notesteme/internal/apierr"
)

// i18nSite 是一個建立錯誤訊息的呼叫點：fn 是函式名稱，idx 是訊息參數的位置。
var i18nMessageFuncs = map[string]int{
	"newError":     2,
	"errForbidden": 0,
	"invalidInput": 0,
	"batchInvalid": 1,
	"invalid":      0, // upload.go 的 readUpload 中的區域函式
	"fieldsError":  2,
}

// i18nTemplateFuncs 是包住 apierr.Newf 的 helper：範本名稱的位置。
var i18nTemplateFuncs = map[string]int{"batchInvalidf": 1}

// NFR-I18N-2：internal/api 中每一個建立錯誤訊息的地方都要走目錄。訊息參數依形式歸類，任何一處無法歸類都會失敗：
//
//   - 字串常值、套件中的字串常數：要在目錄中（apierr.Translate 換得出 English）。
//   - apierr.Newf：範本要存在（範本的三種語言由 apierr 的 TestCatalogComplete 檢查）。
//   - sentinel 錯誤：pkg.ErrX.Error()，或 err.Error() 位在 errors.Is(err, pkg.ErrX) 的 case／if 之下；
//     名稱要在 i18nSentinels 中（值的文字要在目錄中）。
//   - 型別化錯誤：x.Error()、x.Msg、x.Message，x 宣告成 *pkg.T（errors.As 的目標）；型別要在 i18nTyped 中，
//     並附上代表性的值（文字要在目錄中；欄位錯誤的說明另由 apierr 的 TestDomainTextsAreInCatalog 掃描 domain 套件）。
//   - 其他：寫在 i18nExempt（「檔案:運算式」→ 理由），例如外部服務帶回的原文已包在翻譯過的外層句子裡。
func TestAPIErrorLiteralsAreInCatalog(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("找不到 internal/api：%v", err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	consts := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, file)
		collectStringConsts(file, consts)
	}
	counts := map[string]int{}
	usedSentinel, usedTyped, usedExempt := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var problems []string
	for _, file := range parsed {
		walkWithStack(file, func(n ast.Node, stack []ast.Node) {
			// handler 自己寫的欄位錯誤說明（422 的 message 是第一個欄位的「路徑：說明」，說明要能翻譯）
			if cl, ok := n.(*ast.CompositeLit); ok && isFieldErrorLit(cl, stack) {
				for _, el := range cl.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Message" {
						if lit, ok := kv.Value.(*ast.BasicLit); ok {
							s, _ := strconv.Unquote(lit.Value)
							pos := fset.Position(lit.Pos())
							counts["欄位說明"]++
							if en, ok := apierr.Translate(s, apierr.LangEn); !ok || en == s {
								problems = append(problems, filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line)+" 欄位說明不在目錄中："+s)
							}
						}
					}
				}
				return
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return
			}
			pos := fset.Position(call.Pos())
			site := filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "apierr" {
					switch sel.Sel.Name {
					case "Newf":
						counts["範本"]++
						if lit, ok := call.Args[2].(*ast.BasicLit); ok {
							k, _ := strconv.Unquote(lit.Value)
							if !apierr.HasTemplate(k) {
								problems = append(problems, site+" 沒有這個範本："+k)
							}
						}
						return
					case "New":
						classifyMessage(fset, site, call.Args[2], stack, consts, counts, usedSentinel, usedTyped, usedExempt, &problems)
					}
				}
				return
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return
			}
			if ti, ok := i18nTemplateFuncs[id.Name]; ok && len(call.Args) > ti {
				counts["範本"]++
				if lit, ok := call.Args[ti].(*ast.BasicLit); ok {
					k, _ := strconv.Unquote(lit.Value)
					if !apierr.HasTemplate(k) {
						problems = append(problems, site+" 沒有這個範本："+k)
					}
				}
				return
			}
			idx, ok := i18nMessageFuncs[id.Name]
			if !ok || len(call.Args) <= idx {
				return
			}
			classifyMessage(fset, site, call.Args[idx], stack, consts, counts, usedSentinel, usedTyped, usedExempt, &problems)
		})
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("未走目錄：%s", p)
	}
	for k := range i18nSentinels {
		if !usedSentinel[k] {
			t.Errorf("i18nSentinels 的 %s 已經沒有用到，請移除", k)
		}
	}
	for k := range i18nTyped {
		if !usedTyped[k] {
			t.Errorf("i18nTyped 的 %s 已經沒有用到，請移除", k)
		}
	}
	for k := range i18nExempt {
		if !usedExempt[k] {
			t.Errorf("i18nExempt 的 %s 已經沒有用到，請移除", k)
		}
	}
	t.Logf("internal/api 的錯誤訊息：常值與格式 %d、常數 %d、範本 %d、sentinel %d、型別化錯誤 %d、例外 %d、欄位說明 %d；未走目錄 %d",
		counts["常值"]+counts["格式"], counts["常數"], counts["範本"], counts["sentinel"], counts["型別"], counts["例外"], counts["欄位說明"], len(problems))
}

// 訊息參數本身就是參數或區域變數的轉手（helper 自己的定義）時不計。
var i18nPassThrough = map[string]bool{"message": true, "msg": true}

func classifyMessage(fset *token.FileSet, site string, arg ast.Expr, stack []ast.Node, consts map[string]string,
	counts map[string]int, usedSentinel, usedTyped, usedExempt map[string]bool, problems *[]string) {
	file := strings.SplitN(site, ":", 2)[0]
	expr := exprText(fset, arg)
	unclassified := " 無法歸類：" + expr
	check := func(kind, s string) {
		counts[kind]++
		if en, ok := apierr.Translate(s, apierr.LangEn); !ok || en == s {
			*problems = append(*problems, site+" 不在目錄中："+s)
		}
	}
	switch a := arg.(type) {
	case *ast.BasicLit:
		s, _ := strconv.Unquote(a.Value)
		check("常值", s)
		return
	case *ast.Ident:
		if s, ok := consts[a.Name]; ok {
			check("常數", s)
			return
		}
		if i18nPassThrough[a.Name] && enclosingFuncDefines(stack, a.Name) {
			return
		}
		// 區域變數：所在函式中指定給它的每一個值都要能歸類
		if rhs := localValues(stack, a.Name); len(rhs) > 0 {
			before := len(*problems)
			for _, r := range rhs {
				classifyMessage(fset, site, r.expr, r.stack, consts, counts, usedSentinel, usedTyped, usedExempt, problems)
			}
			if len(*problems) == before {
				return
			}
			return
		}
	case *ast.SelectorExpr:
		// defaultMessages[...].msg 是 DefaultMessages 的訊息，已在目錄中
		if ix, ok := a.X.(*ast.IndexExpr); ok && a.Sel.Name == "msg" {
			if id, ok := ix.X.(*ast.Ident); ok && id.Name == "defaultMessages" {
				counts["常值"]++
				return
			}
		}
		if x, ok := a.X.(*ast.Ident); ok && (a.Sel.Name == "Msg" || a.Sel.Name == "Message") {
			if typ := declaredType(stack, x.Name); typ != "" {
				if _, ok := i18nTyped[typ]; ok {
					usedTyped[typ] = true
					counts["型別"]++
					return
				}
				*problems = append(*problems, site+" 型別化錯誤 "+typ+" 不在 i18nTyped："+expr)
				return
			}
		}
		// 套件的 sentinel 文字常數（例如 hubclient.MsgNotConfigured）
		if x, ok := a.X.(*ast.Ident); ok {
			name := x.Name + "." + a.Sel.Name
			if _, ok := i18nSentinels[name]; ok {
				usedSentinel[name] = true
				counts["sentinel"]++
				return
			}
		}
	case *ast.CallExpr:
		sel, ok := a.Fun.(*ast.SelectorExpr)
		// fmt.Sprintf("格式", …)：格式要在 apierr 的 domain 格式目錄中
		if ok && sel.Sel.Name == "Sprintf" && len(a.Args) > 0 {
			if x, isPkg := sel.X.(*ast.Ident); isPkg && x.Name == "fmt" {
				if lit, isLit := a.Args[0].(*ast.BasicLit); isLit {
					f, _ := strconv.Unquote(lit.Value)
					counts["格式"]++
					if !apierr.HasFormat(f) {
						*problems = append(*problems, site+" 格式不在目錄中："+f)
					}
					return
				}
			}
		}
		if ok && sel.Sel.Name == "Error" && len(a.Args) == 0 {
			// pkg.ErrX.Error()
			if inner, ok := sel.X.(*ast.SelectorExpr); ok {
				if pkg, ok := inner.X.(*ast.Ident); ok {
					name := pkg.Name + "." + inner.Sel.Name
					if _, ok := i18nSentinels[name]; ok {
						usedSentinel[name] = true
						counts["sentinel"]++
						return
					}
					unclassified = " sentinel " + name + " 不在 i18nSentinels"
				}
			}
			if x, ok := sel.X.(*ast.Ident); ok {
				if typ := declaredType(stack, x.Name); typ != "" {
					if _, ok := i18nTyped[typ]; ok {
						usedTyped[typ] = true
						counts["型別"]++
						return
					}
					*problems = append(*problems, site+" 型別化錯誤 "+typ+" 不在 i18nTyped："+expr)
					return
				}
				// err.Error() 在 errors.Is(err, pkg.ErrX) 之下
				if names := guardingSentinels(stack, x.Name); len(names) > 0 {
					missing := ""
					for _, name := range names {
						if _, ok := i18nSentinels[name]; !ok {
							missing = name
							break
						}
					}
					if missing == "" {
						for _, name := range names {
							usedSentinel[name] = true
						}
						counts["sentinel"]++
						return
					}
					unclassified = " sentinel " + missing + " 不在 i18nSentinels"
				}
			}
		}
	}
	// 其他方式都無法歸類時才查例外清單（每一則都寫明理由與檢查它的測試）
	if _, ok := i18nExempt[file+":"+expr]; ok {
		usedExempt[file+":"+expr] = true
		counts["例外"]++
		return
	}
	*problems = append(*problems, site+unclassified)
}

// collectStringConsts 收集套件層級的字串常數（const x = "…"）。
func collectStringConsts(f *ast.File, out map[string]string) {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
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

// walkWithStack 是帶著祖先節點的 ast.Inspect。
func walkWithStack(root ast.Node, fn func(n ast.Node, stack []ast.Node)) {
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

// declaredType 找出 name 在所在函式中宣告的型別（var name *pkg.T），形式是「pkg.T」；找不到時是空字串。
func declaredType(stack []ast.Node, name string) string {
	for i := len(stack) - 1; i >= 0; i-- {
		var body ast.Node
		switch f := stack[i].(type) {
		case *ast.FuncDecl:
			body = f.Body
		case *ast.FuncLit:
			body = f.Body
		default:
			continue
		}
		typ := ""
		ast.Inspect(body, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok || typ != "" {
				return typ == ""
			}
			for _, id := range vs.Names {
				if id.Name == name {
					if st, ok := vs.Type.(*ast.StarExpr); ok {
						typ = typeString(st.X)
					}
				}
			}
			return true
		})
		if typ != "" {
			return typ
		}
	}
	return ""
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	}
	return ""
}

// guardingSentinels 回傳包住這個節點的 case 或 if 中 errors.Is(name, pkg.ErrX) 的 pkg.ErrX。
func guardingSentinels(stack []ast.Node, name string) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		var conds []ast.Expr
		switch s := stack[i].(type) {
		case *ast.CaseClause:
			conds = s.List
		case *ast.IfStmt:
			conds = []ast.Expr{s.Cond}
		case *ast.FuncDecl, *ast.FuncLit:
			return nil
		default:
			continue
		}
		var names []string
		for _, c := range conds {
			ast.Inspect(c, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Is" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "errors" {
					return true
				}
				if a, ok := call.Args[0].(*ast.Ident); !ok || a.Name != name {
					return true
				}
				if s := typeString(call.Args[1]); s != "" {
					names = append(names, s)
				}
				return true
			})
		}
		if len(names) > 0 {
			return names
		}
	}
	return nil
}

// enclosingFuncDefines 回報 name 是不是所在函式的參數（helper 的定義本身）。
func enclosingFuncDefines(stack []ast.Node, name string) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		var ft *ast.FuncType
		switch f := stack[i].(type) {
		case *ast.FuncDecl:
			ft = f.Type
		case *ast.FuncLit:
			ft = f.Type
		default:
			continue
		}
		for _, p := range ft.Params.List {
			for _, n := range p.Names {
				if n.Name == name {
					return true
				}
			}
		}
		return false
	}
	return false
}

func exprText(fset *token.FileSet, e ast.Expr) string {
	start, end := fset.Position(e.Pos()), fset.Position(e.End())
	b, err := os.ReadFile(start.Filename)
	if err != nil || end.Offset > len(b) {
		return "?"
	}
	return string(b[start.Offset:end.Offset])
}

type localValue struct {
	expr  ast.Expr
	stack []ast.Node
}

// localValues 是所在函式中指定給區域變數 name 的值（name := …、name = …）。
func localValues(stack []ast.Node, name string) []localValue {
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
		var out []localValue
		walkWithStack(body, func(n ast.Node, st []ast.Node) {
			a, ok := n.(*ast.AssignStmt)
			if !ok || len(a.Lhs) != len(a.Rhs) {
				return
			}
			for j, l := range a.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == name {
					out = append(out, localValue{a.Rhs[j], append(append([]ast.Node{}, stack[:i+1]...), st...)})
				}
			}
		})
		return out
	}
	return nil
}

// isFieldErrorLit 回報組合常值是不是 validate.FieldError（含 []validate.FieldError{{…}} 省略型別的元素）。
func isFieldErrorLit(cl *ast.CompositeLit, stack []ast.Node) bool {
	if cl.Type != nil {
		return typeString(cl.Type) == "validate.FieldError"
	}
	for i := len(stack) - 1; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.KeyValueExpr:
			continue
		case *ast.CompositeLit:
			if at, ok := p.Type.(*ast.ArrayType); ok {
				return typeString(at.Elt) == "validate.FieldError"
			}
			return false
		default:
			return false
		}
	}
	return false
}
