package auth

import "testing"

// 畸形或超出上限的參數必須回傳錯誤，不可 panic（t=0、p=0 會讓 argon2 panic）或耗盡記憶體。
func TestVerifyPasswordRejectsMalformedParams(t *testing.T) {
	good, err := hashPassword("pw", TestParams)
	if err != nil {
		t.Fatal(err)
	}
	parts := splitHash(t, good)
	salt, key := parts[4], parts[5]

	for name, params := range map[string]string{
		"t=0":       "m=8,t=0,p=1",
		"p=0":       "m=8,t=1,p=0",
		"m=0":       "m=0,t=1,p=1",
		"t 過大":      "m=8,t=1000000,p=1",
		"p 過大":      "m=8,t=1,p=255",
		"memory 過大": "m=4294967295,t=1,p=1",
	} {
		encoded := "$argon2id$v=19$" + params + "$" + salt + "$" + key
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic: %v", name, r)
				}
			}()
			if ok, err := verifyPassword("pw", encoded); err == nil || ok {
				t.Errorf("%s: ok=%v err=%v, want error", name, ok, err)
			}
		}()
	}
}

func splitHash(t *testing.T, encoded string) []string {
	t.Helper()
	var parts []string
	start := 0
	for i := 0; i < len(encoded); i++ {
		if encoded[i] == '$' {
			parts = append(parts, encoded[start:i])
			start = i + 1
		}
	}
	return append(parts, encoded[start:])
}
