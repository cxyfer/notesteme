package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashParams 是 argon2id 的成本參數。
type HashParams struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
	KeyLen  uint32
	SaltLen int
}

// ProductionParams 是 production 使用的參數。不得在 production 路徑降低；
// 只有測試透過 Config.Params 傳入較低成本的 TestParams。
var ProductionParams = HashParams{Time: 3, Memory: 64 * 1024, Threads: 2, KeyLen: 32, SaltLen: 16}

// TestParams 是測試用的低成本參數，只供驗收測試與單元測試使用。
var TestParams = HashParams{Time: 1, Memory: 8, Threads: 1, KeyLen: 32, SaltLen: 16}

// hashPassword 回傳 PHC 格式的 argon2id hash：$argon2id$v=19$m=…,t=…,p=…$<salt>$<hash>。
func hashPassword(password string, p HashParams) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// 驗證時接受的 hash 參數上限（遠高於 ProductionParams）。
const (
	maxVerifyMemory  = 256 * 1024 // KiB
	maxVerifyTime    = 16
	maxVerifyThreads = 16
)

var errBadHash =errors.New("auth: malformed password hash")

// verifyPassword 比對密碼與 PHC 格式的 hash（參數取自 hash 本身）。
func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errBadHash
	}
	// 參數來自 DB，可能被竄改：0 會讓 argon2 panic，過大則耗盡記憶體或 CPU。
	if memory < 1 || memory > maxVerifyMemory ||
		time < 1 || time > maxVerifyTime ||
		threads < 1 || threads > maxVerifyThreads {
		return false, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
