package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// builtinFS 是內建 prompt 的內文：builtin/<id>/v<N>.md（D12）。
//
//go:embed builtin
var builtinFS embed.FS

// builtinPrompt 是一個內建 prompt 與它的所有版本（依版本遞增）。
type builtinPrompt struct {
	prompt   Prompt
	versions []Version
}

// builtins 在程式啟動時從 embed 讀入；內容有誤（缺檔、用到未定義的變數）直接 panic，
// 這是發布前就會被測試抓到的程式錯誤。
var builtins = loadBuiltins()

// builtinKind 由 id 決定內建 prompt 的類型：localize-* 屬於 translate。
func builtinKind(id string) string {
	switch {
	case strings.HasPrefix(id, "segment-"):
		return "segment"
	case strings.HasPrefix(id, "correct-"):
		return "correct"
	case strings.HasPrefix(id, "translate-"), strings.HasPrefix(id, "localize-"):
		return "translate"
	case strings.HasPrefix(id, "chapters-"):
		return "chapters"
	case strings.HasPrefix(id, "frames-"):
		return "frames"
	}
	return ""
}

func loadBuiltins() map[string]builtinPrompt {
	out := make(map[string]builtinPrompt, len(BuiltinIDs))
	epoch := time.Unix(0, 0).UTC()
	for _, id := range BuiltinIDs {
		entries, err := fs.ReadDir(builtinFS, "builtin/"+id)
		if err != nil {
			panic(fmt.Errorf("prompts: 找不到內建 prompt %s：%w", id, err))
		}
		var vs []Version
		for _, e := range entries {
			name := e.Name()
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "v"), ".md"))
			if err != nil || !strings.HasPrefix(name, "v") || !strings.HasSuffix(name, ".md") || n < 1 {
				panic(fmt.Sprintf("prompts: 內建 prompt %s 的檔名 %q 不是 v<N>.md", id, name))
			}
			raw, err := fs.ReadFile(builtinFS, "builtin/"+id+"/"+name)
			if err != nil {
				panic(err)
			}
			body := normalizeBody(string(raw))
			used, undefined, err := ParseVariables(body)
			if err != nil || len(undefined) > 0 {
				panic(fmt.Sprintf("prompts: 內建 prompt %s v%d 的變數不正確：%v %v", id, n, undefined, err))
			}
			vs = append(vs, Version{PromptID: id, Version: n, Body: body, SHA256: BodySHA256(body), Variables: used, CreatedAt: epoch})
		}
		if len(vs) == 0 {
			panic("prompts: 內建 prompt " + id + " 沒有任何版本")
		}
		sort.Slice(vs, func(i, j int) bool { return vs[i].Version < vs[j].Version })
		for i, v := range vs {
			if v.Version != i+1 {
				panic(fmt.Sprintf("prompts: 內建 prompt %s 的版本不連續（缺 v%d）", id, i+1))
			}
		}
		latest := vs[len(vs)-1].Version
		out[id] = builtinPrompt{
			prompt: Prompt{
				ID: id, Kind: builtinKind(id), Builtin: true, DefaultVersion: latest, LatestVersion: latest,
				CreatedAt: epoch, UpdatedAt: epoch,
			},
			versions: vs,
		}
	}
	return out
}

// isBuiltin 回傳 id 是不是內建 prompt。
func isBuiltin(id string) bool { return slices.Contains(BuiltinIDs, id) }
