package faultpoint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// goTool 回傳 go 指令的路徑；找不到時跳過測試。
func goTool(t *testing.T) string {
	t.Helper()
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(gobin); err == nil {
		return gobin
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("找不到 go 指令")
	}
	return gobin
}

// goList 執行 go list（在 repo 根目錄），回傳輸出的各行。
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command(goTool(t), append([]string{"list"}, args...)...)
	cmd.Dir = filepath.Join("..", "..")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v：%v\n%s", args, err, stderr.String())
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// TestProductionBuildExcludesFaultInjection：一般建置的 faultpoint 只有空的 Hit（off.go），
// 正式 binary（cmd/notesteme）依賴的 faultpoint 也是那一份；faultinject 建置才換成 on.go（NFR-REL-1）。
func TestProductionBuildExcludesFaultInjection(t *testing.T) {
	const pkg = "github.com/cxyfer/notesteme/internal/faultpoint"
	files := func(tags ...string) string {
		args := append(tags, "-f", "{{join .GoFiles \",\"}}", "./internal/faultpoint")
		return goList(t, args...)[0]
	}
	if got := files(); got != "faultpoint.go,off.go" {
		t.Errorf("一般建置的 GoFiles = %q，應該只有 faultpoint.go,off.go", got)
	}
	if got := files("-tags", "faultinject"); runtime.GOOS != "windows" && got != "faultpoint.go,on.go" {
		t.Errorf("faultinject 建置的 GoFiles = %q，應該是 faultpoint.go,on.go", got)
	}
	found := false
	for _, line := range goList(t, "-deps", "-f", "{{.ImportPath}} {{join .GoFiles \",\"}}", "./cmd/notesteme") {
		path, gofiles, _ := strings.Cut(line, " ")
		if path != pkg {
			continue
		}
		found = true
		if strings.Contains(gofiles, "on.go") {
			t.Errorf("正式 binary 的 faultpoint 含 on.go：%s", gofiles)
		}
	}
	if !found {
		t.Errorf("cmd/notesteme 的依賴中沒有 %s：故障點沒有接上 pipeline", pkg)
	}
}

// TestProductionBinaryHasNoFaultpointSymbols：實際建置正式 binary，符號表中沒有 faultpoint 的任何東西
// （Hit 被內聯成空的、guard 在 Enabled 後面的程式碼被刪掉）。-short 時跳過（要建置整個 binary）。
func TestProductionBinaryHasNoFaultpointSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("-short：不建置正式 binary")
	}
	gobin := goTool(t)
	bin := filepath.Join(t.TempDir(), "notesteme")
	build := exec.Command(gobin, "build", "-o", bin, "./cmd/notesteme")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build：%v\n%s", err, out)
	}
	out, err := exec.Command(gobin, "tool", "nm", bin).Output()
	if err != nil {
		t.Fatalf("go tool nm：%v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "faultpoint") {
			t.Errorf("正式 binary 含故障注入的符號：%s", line)
		}
	}
}

// TestOnlyFaultpointUsesTheTag：faultinject tag 只能出現在 faultpoint 套件與測試檔，
// 其他 production 檔案不能藏著只有 tag 建置才有的程式碼。
func TestOnlyFaultpointUsesTheTag(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".worktrees", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(b, []byte("faultinject")) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		switch filepath.ToSlash(rel) {
		case "internal/faultpoint/faultpoint.go", "internal/faultpoint/off.go", "internal/faultpoint/on.go",
			"features/fault/doc.go": // 套件說明（沒有程式碼）
			return nil
		}
		t.Errorf("%s 提到 faultinject：故障注入的程式碼只能放在 internal/faultpoint 與測試檔", rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestName(t *testing.T) {
	if got := Name(StageCommit, "transcript"); got != "stage.commit/transcript" {
		t.Errorf("Name = %q", got)
	}
	if got := Name(ArtifactRenamed, ""); got != "artifact.renamed" {
		t.Errorf("Name = %q", got)
	}
}
