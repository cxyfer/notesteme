package notes

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"
)

// BuiltinTemplateID 是內建模板（NOTE-R11）：不能修改，只能複製。
const BuiltinTemplateID = "video-notes"

// BuiltinTemplateBody 是內建模板 video-notes 的內文（NOTE 規格「內建模板的版面」，N1）。
const BuiltinTemplateBody = `{{.Frontmatter}}
# {{.Title}}
{{if .ReadButton}}
{{.ReadButton}}
{{end}}
{{.InfoTable}}

{{.Sections}}

{{.SelfCheck}}
`

// MaxTemplateBody 是模板內文的上限（位元組）。
const MaxTemplateBody = 64 << 10

// templateFields 是模板可以使用的變數（NOTE-R11）。
var templateFields = []string{
	"Frontmatter", "Title", "ReadButton", "InfoTable", "Sections", "SelfCheck", "Lang",
	"Item.URL", "Item.Channel", "Item.Published", "Item.Duration",
}

// TemplateError 是模板不合法：Message 是給使用者看的訊息（422 的 errors[].message，path 是 body）。
type TemplateError struct{ Message string }

func (e *TemplateError) Error() string { return e.Message }

var lineRe = regexp.MustCompile(`^template: [^:]*:(\d+):(?:\d+:)?\s*(.*)$`)

// ParseTemplate 解析並檢查模板（NOTE-R11）：只能用 templateFields 列出的變數，不能用 template、define、block、
// call、range、with 與變數宣告；語法錯誤的訊息指出行號。
func ParseTemplate(body string) (*template.Template, error) {
	if len(body) > MaxTemplateBody {
		return nil, &TemplateError{Message: fmt.Sprintf("模板超過 %d KiB。", MaxTemplateBody>>10)}
	}
	if strings.TrimSpace(body) == "" {
		return nil, &TemplateError{Message: "模板是空的。"}
	}
	t, err := template.New("body").Option("missingkey=error").Parse(body)
	if err != nil {
		msg := err.Error()
		if m := lineRe.FindStringSubmatch(msg); m != nil {
			return nil, &TemplateError{Message: fmt.Sprintf("模板第 %s 行有語法錯誤：%s。", m[1], m[2])}
		}
		return nil, &TemplateError{Message: fmt.Sprintf("模板有語法錯誤：%s。", msg)}
	}
	if len(t.Templates()) > 1 {
		return nil, &TemplateError{Message: "模板不能使用 define 或 block。"}
	}
	if t.Tree == nil || t.Tree.Root == nil {
		return nil, &TemplateError{Message: "模板是空的。"}
	}
	if err := checkNode(t.Tree, t.Tree.Root); err != nil {
		return nil, err
	}
	return t, nil
}

func lineOf(tree *parse.Tree, n parse.Node) string {
	loc, _ := tree.ErrorContext(n)
	// loc 是 "body:<行>:<欄>"。
	parts := strings.Split(loc, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "?"
}

func checkNode(tree *parse.Tree, n parse.Node) error {
	switch n := n.(type) {
	case nil:
		return nil
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Nodes {
			if err := checkNode(tree, c); err != nil {
				return err
			}
		}
	case *parse.TextNode, *parse.CommentNode:
	case *parse.ActionNode:
		return checkPipe(tree, n.Pipe)
	case *parse.IfNode:
		if err := checkPipe(tree, n.Pipe); err != nil {
			return err
		}
		if err := checkNode(tree, n.List); err != nil {
			return err
		}
		if n.ElseList != nil {
			return checkNode(tree, n.ElseList)
		}
	case *parse.RangeNode:
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能使用 range。", lineOf(tree, n))}
	case *parse.WithNode:
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能使用 with。", lineOf(tree, n))}
	case *parse.TemplateNode:
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能使用 template。", lineOf(tree, n))}
	default:
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不支援的語法。", lineOf(tree, n))}
	}
	return nil
}

func checkPipe(tree *parse.Tree, p *parse.PipeNode) error {
	if p == nil {
		return nil
	}
	if len(p.Decl) > 0 {
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能宣告變數。", lineOf(tree, p))}
	}
	for _, c := range p.Cmds {
		for _, a := range c.Args {
			if err := checkArg(tree, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkArg(tree *parse.Tree, a parse.Node) error {
	switch a := a.(type) {
	case *parse.FieldNode:
		return checkField(strings.Join(a.Ident, "."))
	case *parse.VariableNode:
		// 只允許 $（根），$.X 同 .X。
		if len(a.Ident) == 0 || a.Ident[0] != "$" {
			return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能使用變數 %s。", lineOf(tree, a), strings.Join(a.Ident, "."))}
		}
		if len(a.Ident) > 1 {
			return checkField(strings.Join(a.Ident[1:], "."))
		}
	case *parse.ChainNode:
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不支援的語法。", lineOf(tree, a))}
	case *parse.IdentifierNode:
		if a.Ident == "call" {
			return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能使用 call。", lineOf(tree, a))}
		}
	case *parse.PipeNode:
		return checkPipe(tree, a)
	case *parse.DotNode:
		return &TemplateError{Message: fmt.Sprintf("模板第 %s 行：不能直接輸出 .，請指定變數。", lineOf(tree, a))}
	}
	return nil
}

func checkField(path string) error {
	if !slices.Contains(templateFields, path) {
		return &TemplateError{Message: fmt.Sprintf("不認識的變數 .%s。", path)}
	}
	return nil
}

// Render 以模板產生筆記。模板必須已經通過 ParseTemplate；輸出的換行統一成 \n，結尾只有一個換行。
func Render(body string, d Data) (string, error) {
	t, err := ParseTemplate(body)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		var te *TemplateError
		if errors.As(err, &te) {
			return "", te
		}
		return "", &TemplateError{Message: fmt.Sprintf("模板無法套用：%s。", err.Error())}
	}
	out := strings.ReplaceAll(buf.String(), "\r\n", "\n")
	return strings.TrimRight(out, "\n") + "\n", nil
}
