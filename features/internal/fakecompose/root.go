package fakecompose

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindRoot 從 start 往上找含 go.mod 的目錄，作為 repo 根目錄（場景裡的 -f 路徑相對於它）。
// 測試的工作目錄是各自的 package 目錄（例如 features/），所以不能假設 cwd 就是根目錄。
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("從 %s 往上找不到 go.mod，無法決定 repo 根目錄", start)
		}
		dir = parent
	}
}
