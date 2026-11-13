//go:build !linux

package app

import "net"

// sockPathMax 是 Unix socket 路徑的長度上限：BSD 與 macOS 的 sun_path 只有 104 位元組，含結尾的 NUL。
const sockPathMax = 103

// checkPeer 在 Linux 以外沒有 SO_PEERCRED：只靠資料目錄（0700）與 socket 檔（0600）的權限限制存取。
func checkPeer(net.Conn) error { return nil }
