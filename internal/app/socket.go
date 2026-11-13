package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
)

// socketName 是 serve 的本機 Unix socket 在資料目錄下的檔名（ADR-0007）。
const socketName = "notesteme.sock"

// socketPerm 是 socket 檔的權限：只有擁有者能連。
const socketPerm = 0o600

// socketPath 回傳資料目錄的 socket 路徑。
func socketPath(dataDir string) string { return filepath.Join(dataDir, socketName) }

// listenSocket 在資料目錄建立本機 Unix socket，給 client 模式的 CLI 使用。
// 呼叫端已經持有資料目錄的 flock，所以遺留的 socket 檔一定是上一個程序沒清掉的（例如被 Kill），可以直接刪除。
//
// 存取控制有兩層：資料目錄是 0700、socket 檔是 0600；Linux 上另外檢查對端的 uid（SO_PEERCRED），
// 只接受同一個使用者或 root。通過的連線以 owner 身分執行（api.NewLocal），所以這裡不能放寬。
//
// 路徑長度受 sockaddr_un 限制（Linux 約 108 位元組）：超過時回傳說明原因的錯誤，不會靜默略過。
func listenSocket(env Env, dataDir string, log *slog.Logger) (net.Listener, error) {
	path := socketPath(dataDir)
	if len(path) > sockPathMax {
		return nil, fmt.Errorf("本機 socket 的路徑 %s 太長（%d 位元組，上限 %d）；請改用較短的資料目錄（NOTESTEME_DATA）。",
			path, len(path), sockPathMax)
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := env.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("監聽本機 socket %s 失敗：%w", path, err)
	}
	// 這一定要是 Unix socket：其他種類的 listener 不受檔案權限與 uid 檢查保護，
	// 卻會以 owner 身分服務請求。
	if ln.Addr().Network() != "unix" {
		_ = ln.Close()
		return nil, fmt.Errorf("本機 socket 必須是 unix listener，得到 %s", ln.Addr().Network())
	}
	if err := os.Chmod(path, socketPerm); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("設定本機 socket %s 的權限失敗：%w", path, err)
	}
	return &peerListener{Listener: ln, log: log}, nil
}

// removeStaleSocket 刪除上次留下的 socket 檔；路徑上是其他種類的檔案時不刪，回傳錯誤。
func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("檢查 %s 失敗：%w", path, err)
	case fi.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("%s 已經存在而且不是 socket，不會刪除；請自行處理後再啟動。", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("刪除上次留下的 %s 失敗：%w", path, err)
	}
	return nil
}

// removeSocket 在正常關閉時移除 socket 檔；已經不存在不算錯誤。
func removeSocket(dataDir string) { _ = os.Remove(socketPath(dataDir)) }

// keepSocket 讓 ln 關閉時不要刪除 socket 檔（模擬程序中斷：Kill 不清理，下次啟動時才清）。
func keepSocket(ln net.Listener) {
	if k, ok := ln.(interface{ SetUnlinkOnClose(bool) }); ok {
		k.SetUnlinkOnClose(false)
	}
}

// peerListener 在 Accept 時檢查對端身分（checkPeer，依平台而不同），不符的連線直接關閉。
type peerListener struct {
	net.Listener
	log *slog.Logger
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := checkPeer(c); err != nil {
			l.log.Warn("拒絕本機 socket 的連線", "error", err)
			_ = c.Close()
			continue
		}
		return c, nil
	}
}

// SetUnlinkOnClose 轉給底層的 *net.UnixListener。
func (l *peerListener) SetUnlinkOnClose(unlink bool) {
	if u, ok := l.Listener.(*net.UnixListener); ok {
		u.SetUnlinkOnClose(unlink)
	}
}
