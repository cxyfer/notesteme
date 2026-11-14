//go:build !unix

package backup

// freeSpace 在非 unix 平台（例如 Windows）沒有 statfs 可用，回報無法得知，Extract 會略過空間檢查；
// 此時只剩 64 GiB 的單一項目上限保護。
func freeSpace(string) (uint64, error) { return 0, ErrSpaceUnknown }
