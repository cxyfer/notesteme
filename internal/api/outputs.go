package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// 這個檔案是輸出檔紀錄的路由（OUT-R11）。

func (rt *routes) registerOutputs(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-outputs", Method: http.MethodGet, Path: "/api/items/{id}/outputs",
		Summary: "輸出目標（sidecar、folder、vault）寫出過的檔案與寫入時的 sha256（OUT-R11、VAULT-R5）",
	}, auth.ScopeRead, rt.listOutputs)
}

type outputsInput struct {
	ID string `path:"id" doc:"影音 ID"`
}

// OutputFile 是寫出的一個檔案。
type OutputFile struct {
	Path      string    `json:"path" doc:"伺服器上的檔案路徑；vault 目標是相對於 vault 根目錄的路徑"`
	Target    string    `json:"target" doc:"輸出目標：sidecar、folder 或 vault"`
	Vault     string    `json:"vault,omitempty" doc:"vault 的 id（vault 目標）"`
	Kind      string    `json:"kind" doc:"檔案的種類：subtitle、note 或 asset（筆記嵌入的畫面）"`
	State     string    `json:"state" doc:"最近一次發布時的結果：written、unchanged、saved_as_new、skipped_user_changed"`
	Commit    string    `json:"commit,omitempty" doc:"git vault 中這個檔案所在的 commit"`
	Format    string    `json:"format"`
	Lang      string    `json:"lang"`
	SHA256    string    `json:"sha256" doc:"notesteme 寫入時的內容雜湊；之後檔案的雜湊與它不同代表被改過"`
	Size      int64     `json:"size"`
	WrittenAt time.Time `json:"written_at"`
}

type outputsOutput struct {
	Body struct {
		Outputs []OutputFile `json:"outputs"`
	}
}

func (rt *routes) listOutputs(ctx context.Context, in *outputsInput) (*outputsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	list, err := rt.d.Pipeline.Outputs(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &outputsOutput{}
	out.Body.Outputs = make([]OutputFile, 0, len(list))
	for _, o := range list {
		out.Body.Outputs = append(out.Body.Outputs, OutputFile{
			Path: o.Path, Target: o.Target, Format: o.Format, Lang: o.Lang, SHA256: o.SHA256, Size: o.Size, WrittenAt: o.WrittenAt,
			Vault: o.Vault, Kind: o.Kind, State: o.State, Commit: o.GitCommit,
		})
	}
	return out, nil
}
