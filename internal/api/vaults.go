package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// 這個檔案是設定檔定義的 vault 清單（VAULT-R1）：只有 id、adapter 與 base，不回傳路徑、remote 或憑證。

// VaultInfo 是一個 vault 的公開資訊。
type VaultInfo struct {
	ID      string `json:"id" doc:"vault 的 id（處理方案的 outputs[].vault）"`
	Adapter string `json:"adapter" doc:"fs（本機資料夾）或 git（推送到 git repo）"`
	Base    string `json:"base" doc:"vault 內的根資料夾"`
}

type vaultsOutput struct {
	Body struct {
		Vaults []VaultInfo `json:"vaults"`
	}
}

func (rt *routes) registerVaults(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "vaults-list", Method: http.MethodGet, Path: "/api/vaults",
		Summary: "列出設定檔定義的 vault（id、adapter、base；VAULT-R1）",
	}, auth.ScopeAdmin, rt.listVaults)
}

func (rt *routes) listVaults(ctx context.Context, _ *struct{}) (*vaultsOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	out := &vaultsOutput{}
	out.Body.Vaults = []VaultInfo{}
	if rt.d.Pipeline == nil {
		return out, nil
	}
	for _, v := range rt.d.Pipeline.Vaults() {
		out.Body.Vaults = append(out.Body.Vaults, VaultInfo{ID: v.ID, Adapter: v.Adapter, Base: v.Base})
	}
	return out, nil
}
