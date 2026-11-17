package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cxyfer/notesteme/internal/secrets"
)

// ServiceSettingsStore 管理外部服務商的 API key（GET／PUT /api/settings/services，SEC-R4、SEC-R6）。
// *secrets.Services 實作它。
type ServiceSettingsStore interface {
	Get(ctx context.Context) (secrets.ServiceSettings, error)
	Put(ctx context.Context, update secrets.ServiceSettingsUpdate) (secrets.ServiceSettings, error)
}

func (rt *routes) getServicesSettings(ctx context.Context) (*settingsOutput, error) {
	if rt.d.Services == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	ss, err := rt.d.Services.Get(ctx)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "讀取服務設定失敗", "error", err)
		return nil, asError(err)
	}
	return &settingsOutput{Body: serviceSettingsMap(ss)}, nil
}

func (rt *routes) putServicesSettings(ctx context.Context, body *map[string]any) (*settingsOutput, error) {
	if rt.d.Services == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	var raw map[string]any
	if body != nil {
		raw = *body
	}
	update, err := decodeServiceSettingsUpdate(raw)
	if err != nil {
		return nil, err
	}
	ss, err := rt.d.Services.Put(ctx, update)
	switch {
	case errors.Is(err, secrets.ErrUnknownProvider):
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error())
	case errors.Is(err, secrets.ErrInvalidAPIKey):
		// 錯誤訊息不得回顯任何 API key 的內容（NFR-SEC-3）。
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, secrets.ErrInvalidAPIKey.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "更新服務設定失敗", "error", err)
		return nil, asError(err)
	}
	return &settingsOutput{Body: serviceSettingsMap(ss)}, nil
}

func decodeServiceSettingsUpdate(body map[string]any) (secrets.ServiceSettingsUpdate, error) {
	if body == nil {
		return secrets.ServiceSettingsUpdate{}, nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return secrets.ServiceSettingsUpdate{}, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "服務設定的格式不正確。")
	}
	var update secrets.ServiceSettingsUpdate
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&update); err != nil {
		// 不回傳 dec.Decode 的錯誤原文，避免萬一包含輸入內容。
		return secrets.ServiceSettingsUpdate{}, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "服務設定包含不支援的欄位或格式不正確。")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return secrets.ServiceSettingsUpdate{}, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "服務設定的格式不正確。")
	}
	return update, nil
}

func serviceSettingsMap(ss secrets.ServiceSettings) map[string]any {
	providers := make(map[string]any, len(ss.Providers))
	for id, info := range ss.Providers {
		p := map[string]any{}
		if info.APIKeyLast4 != "" {
			p["api_key_last4"] = info.APIKeyLast4
		}
		providers[id] = p
	}
	return map[string]any{"providers": providers}
}
