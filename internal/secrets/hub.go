package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cxyfer/notesteme/internal/store"
)

// Hub token 在 secrets 表中的 kind 與 name（HSHR-R1、ADR-0032 第 4 點）。AAD 為 "hub/token"。
const (
	HubKind      = "hub"
	HubTokenName = "token"
	// maxHubToken 是 Hub token 的長度上限（Hub 的 PAT 是 nsh_ 加 40 字元，留餘裕給之後的格式）。
	maxHubToken = 512
)

// ErrInvalidHubToken 表示 Hub token 含控制字元或太長；訊息永遠不含 token 的內容。
var ErrInvalidHubToken = errors.New("Hub token 格式不正確。")

// HubToken 讀取並解密 Hub token；沒有設定時 ok 為 false。
func (s *Services) HubToken(ctx context.Context) (string, bool, error) {
	sec, err := s.st.Read().Secret(ctx, HubKind, HubTokenName)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plain, err := s.keys.Open(HubKind, HubTokenName, sec.Nonce, sec.Ciphertext)
	if err != nil {
		return "", false, fmt.Errorf("解開 Hub token 失敗：%w", err)
	}
	return string(plain), len(plain) > 0, nil
}

// SetHubToken 加密儲存 Hub token；空字串刪除。
func (s *Services) SetHubToken(ctx context.Context, token string) error {
	if token == "" {
		_, err := s.st.Write().DeleteSecret(ctx, HubKind, HubTokenName)
		return err
	}
	if len(token) > maxHubToken || hasControlChar(token) || token != strings.TrimSpace(token) {
		return ErrInvalidHubToken
	}
	nonce, ct, err := s.keys.Seal(HubKind, HubTokenName, []byte(token))
	if err != nil {
		return fmt.Errorf("加密 Hub token 失敗：%w", err)
	}
	return s.st.Write().UpsertSecret(ctx, HubKind, HubTokenName, nonce, ct)
}
