// Package secrets 處理信封加密的金鑰層（SEC-R4、SEC-R5、ADR-0013）：
// 隨機的資料金鑰存在 DB，並由 master key（NOTESTEME_MASTER_KEY）加密。
//
// 做到「第一次啟動建立資料金鑰、之後啟動以 master key 解開」（master key 錯誤時拒絕啟動），
// 以及以資料金鑰加解密單筆 secret（Keyring.Seal、Keyring.Open，見 seal.go）。
// TODO(SEC-R6、SEC-R8、SEC-R9)：API key 只回末四碼、輪替 master key、reset。
package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/cxyfer/notesteme/internal/store"
)

// ErrWrongMasterKey 表示 master key 無法解開資料金鑰（SEC-R5）。
var ErrWrongMasterKey = errors.New(
	"master key 不正確，無法解開資料金鑰。請確認 NOTESTEME_MASTER_KEY；" +
		"若 master key 已經遺失，可以執行 notesteme secrets reset 重設（所有 API key 與 cookie 需要重新輸入）。")

// KDFParams 是由密語推導金鑰時使用的 argon2id 參數；會寫進加密後的資料金鑰，所以之後調整不影響既有資料。
type KDFParams struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// ProductionKDF 是 production 使用的參數，不得在 production 路徑降低。
var ProductionKDF = KDFParams{Time: 3, Memory: 64 * 1024, Threads: 2}

// TestKDF 是測試用的低成本參數。
var TestKDF = KDFParams{Time: 1, Memory: 8, Threads: 1}

const (
	keyLen  = 32
	saltLen = 16
	// aad 把密文綁定在用途上，避免被拿去解別的東西。
	aad = "notesteme/data-key/v1"
	// 解析 DB 內的參數時的上限，避免被竄改的資料造成資源耗盡。
	maxKDFMemory  = 1 << 20 // 1 GiB
	maxKDFTime    = 16
	maxKDFThreads = 16
)

// Keyring 持有解開後的資料金鑰（只在記憶體中）。
type Keyring struct {
	dataKey []byte
}

// Open 以 masterKey 取得資料金鑰：DB 還沒有時建立並寫入；已有時解開，失敗回傳 ErrWrongMasterKey。
// masterKey 是 32 bytes 的 base64 時直接作為金鑰，否則用 argon2id 推導（SEC 規格的待決問題暫定做法）。
func Open(ctx context.Context, st *store.Store, masterKey string, p KDFParams) (*Keyring, error) {
	wrapped, ok, err := st.ActiveWrappedKey(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		dk, err := unwrap(masterKey, wrapped)
		if err != nil {
			return nil, err
		}
		return &Keyring{dataKey: dk}, nil
	}

	dk := make([]byte, keyLen)
	if _, err := rand.Read(dk); err != nil {
		return nil, err
	}
	wrapped, err = wrap(masterKey, dk, p)
	if err != nil {
		return nil, err
	}
	inserted, err := st.InsertWrappedKey(ctx, wrapped)
	if err != nil {
		return nil, err
	}
	if !inserted {
		// 同時有別的啟動寫入了金鑰：改用它。
		return Open(ctx, st, masterKey, p)
	}
	return &Keyring{dataKey: dk}, nil
}

// wrap 以 masterKey 加密資料金鑰，格式：v1$m=…,t=…,p=…$<salt>$<nonce>$<密文>。
func wrap(masterKey string, dataKey []byte, p KDFParams) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(deriveKEK(masterKey, salt, p))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := aead.Seal(nil, nonce, dataKey, []byte(aad))
	enc := base64.RawStdEncoding
	return fmt.Sprintf("v1$m=%d,t=%d,p=%d$%s$%s$%s",
		p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(nonce), enc.EncodeToString(ct)), nil
}

func unwrap(masterKey, wrapped string) ([]byte, error) {
	parts := strings.Split(wrapped, "$")
	if len(parts) != 5 || parts[0] != "v1" {
		return nil, errors.New("secrets: 資料金鑰的格式不正確")
	}
	var p KDFParams
	if _, err := fmt.Sscanf(parts[1], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil ||
		p.Memory < 1 || p.Time < 1 || p.Threads < 1 || // 0 會讓 argon2 panic
		p.Memory > maxKDFMemory || p.Time > maxKDFTime || p.Threads > maxKDFThreads {
		return nil, errors.New("secrets: 資料金鑰的參數不正確")
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	nonce, err2 := enc.DecodeString(parts[3])
	ct, err3 := enc.DecodeString(parts[4])
	if err := errors.Join(err1, err2, err3); err != nil {
		return nil, errors.New("secrets: 資料金鑰的格式不正確")
	}
	aead, err := chacha20poly1305.NewX(deriveKEK(masterKey, salt, p))
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("secrets: 資料金鑰的格式不正確")
	}
	dk, err := aead.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return nil, ErrWrongMasterKey
	}
	return dk, nil
}

// deriveKEK 由 master key 取得加密資料金鑰用的金鑰。
func deriveKEK(masterKey string, salt []byte, p KDFParams) []byte {
	if raw, err := base64.StdEncoding.DecodeString(masterKey); err == nil && len(raw) == keyLen {
		return raw
	}
	return argon2.IDKey([]byte(masterKey), salt, p.Time, p.Memory, p.Threads, keyLen)
}
