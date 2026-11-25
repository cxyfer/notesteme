package main

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/compat"
)

// versionVar 是建置時以 -ldflags -X 寫入的程式版本（OPS-R22、CLI-R9）。
const versionVar = "github.com/cxyfer/notesteme/internal/app.version"

// buildVersion 建置指定版本的 notesteme 到 dir。
func buildVersion(t *testing.T, dir, version string) string {
	t.Helper()
	bin := filepath.Join(dir, "notesteme-"+version)
	cmd := exec.Command("go", "build", "-o", bin, "-ldflags", "-X "+versionVar+"="+version, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go build %s 失敗：%v\n%s", version, err, out)
	}
	return bin
}

// NFR-CMP-4（bench）：新版的 CLI 可以連到舊一個小版本的 serve，反之亦然；不相容時明確提示升級哪一邊（CLI-R9）。
// 以 -ldflags -X 建出 0.2.0～0.6.0 五支 binary，serve 用 0.4.0，各版 CLI 經本機 socket 執行 items ls：
// 相差一個小版本（0.3.0、0.5.0）結束碼 0；相差兩個（0.2.0、0.6.0）結束碼 1 並指出要升級哪一邊。
// 沒有版本標頭的請求（例如瀏覽器或 curl 的 /healthz）不受影響，回應也不帶版本。
// 建置五支 binary 需要 go 指令與建置快取，-short 時略過；CI 的 go job 不帶 -short，所以會執行。
func TestCLIAndServeVersionsAcrossBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 不建置 binary")
	}
	// unix socket 的路徑上限約 108 bytes：t.TempDir() 在 CI 上可能太長，改用系統暫存目錄下的短路徑。
	dir, err := os.MkdirTemp("", "nscmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	versions := []string{"0.2.0", "0.3.0", "0.4.0", "0.5.0", "0.6.0"}
	bins := make(map[string]string, len(versions))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, v := range versions {
		wg.Go(func() {
			b := buildVersion(t, dir, v)
			mu.Lock()
			bins[v] = b
			mu.Unlock()
		})
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	data := filepath.Join(dir, "d")
	empty := filepath.Join(dir, "empty")
	for _, d := range []string{data, empty} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	base := []string{"PATH=" + empty, "HOME=" + empty, "NOTESTEME_DATA=" + data}

	// serve 0.4.0：listen 用隨機埠，從 stdout 的「notesteme listening on <addr>」取得位址。
	serve := exec.Command(bins["0.4.0"], "serve")
	serve.Env = append(append([]string{}, base...),
		"NOTESTEME_MASTER_KEY=cmp-4-test-master-key-0123456789", "NOTESTEME_PASSWORD=cmp-4-test-password",
		"NOTESTEME_LISTEN=127.0.0.1:0")
	stdout, err := serve.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	serveLog := &lockedBuffer{}
	serve.Stderr = serveLog
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = serve.Process.Kill()
		_ = serve.Wait()
	})
	addrCh, exited := make(chan string, 1), make(chan struct{})
	go func() {
		defer close(exited) // stdout 關閉：serve 已經結束
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if a, ok := strings.CutPrefix(sc.Text(), "notesteme listening on "); ok {
				addrCh <- strings.TrimSpace(a)
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case <-exited:
		t.Fatalf("serve 啟動失敗\n%s", serveLog.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("serve 沒有在 30 秒內開始服務\n%s", serveLog.String())
	}
	// 本機 socket 在 listen 之前建立；等到它存在（輪詢，不固定等待）。
	sock := filepath.Join(data, "notesteme.sock")
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("找不到本機 socket %s\n%s", sock, serveLog.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	for _, c := range []struct {
		cli     string
		code    int
		message string
	}{
		{"0.2.0", 1, "CLI 版本太舊（CLI 0.2.0，serve 0.4.0），請升級 CLI。"},
		{"0.3.0", 0, ""},
		{"0.4.0", 0, ""},
		{"0.5.0", 0, ""},
		{"0.6.0", 1, "serve 版本太舊（serve 0.4.0，CLI 0.6.0），請升級 serve，或換成 0.4 版的 CLI。"},
	} {
		cmd := exec.Command(bins[c.cli], "items", "ls")
		cmd.Env = base // 沒有 master key：一定是經本機 socket 連到 serve，不會變成獨立模式
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("CLI %s：%v", c.cli, err)
			}
			code = ee.ExitCode()
		}
		if code != c.code {
			t.Errorf("CLI %s → serve 0.4.0：結束碼 %d，預期 %d；stdout %q，stderr %q", c.cli, code, c.code, out.String(), errOut.String())
		}
		if c.message != "" && !strings.Contains(errOut.String(), c.message) {
			t.Errorf("CLI %s：stderr %q 沒有「%s」", c.cli, errOut.String(), c.message)
		}
	}

	// 沒有版本標頭的請求：照常回應，回應不帶版本。
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(compat.Header) != "" {
		t.Errorf("/healthz = %d，%s = %q", resp.StatusCode, compat.Header, resp.Header.Get(compat.Header))
	}
	// 帶版本標頭的請求：回應帶 serve 的版本。
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/healthz", nil)
	req.Header.Set(compat.Header, "0.4.0")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(compat.Header) != "0.4.0" {
		t.Errorf("帶版本的 /healthz = %d，%s = %q", resp.StatusCode, compat.Header, resp.Header.Get(compat.Header))
	}
}

// lockedBuffer 讓 serve 的 stderr 可以在測試失敗時安全讀出（serve 仍在寫）。
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
