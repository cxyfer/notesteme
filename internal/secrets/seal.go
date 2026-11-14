package secrets

import (
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"
)

// ErrDecrypt 表示密文無法解開：被竄改、nonce 不對、AAD（kind/name）不符，或不是用這把資料金鑰加密的。
// 訊息刻意不帶任何金鑰、明文或密文內容（NFR-SEC-3）。
var ErrDecrypt = errors.New("secrets: 無法解開密文")

// secretAAD 把密文綁定在 kind/name 上，避免被搬到另一筆 secret 後仍可解開。
func secretAAD(kind, name string) []byte { return []byte(kind + "/" + name) }

// Seal 以資料金鑰（XChaCha20-Poly1305）加密 plaintext，AAD 為 kind + "/" + name。
// 每次呼叫產生新的隨機 nonce；回傳的 nonce 與密文由呼叫端存進 store（SEC-R4）。
func (k *Keyring) Seal(kind, name string, plaintext []byte) (nonce, ciphertext []byte, err error) {
	aead, err := chacha20poly1305.NewX(k.dataKey)
	if err != nil {
		return nil, nil, errors.New("secrets: 無法初始化加密") // 不包裝原錯誤，避免帶出金鑰相關資訊
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, aead.Seal(nil, nonce, plaintext, secretAAD(kind, name)), nil
}

// Open 解開 Seal 產生的密文；kind、name 必須與加密時相同。任何失敗都回傳 ErrDecrypt。
func (k *Keyring) Open(kind, name string, nonce, ciphertext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(k.dataKey)
	if err != nil {
		return nil, errors.New("secrets: 無法初始化加密")
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrDecrypt
	}
	plain, err := aead.Open(nil, nonce, ciphertext, secretAAD(kind, name))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
