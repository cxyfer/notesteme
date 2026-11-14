// Package ytdlp 管理 yt-dlp 的安裝與更新（OPS-R6～R8）。
//
// 目錄結構（<data>/tools 底下）：
//
//	yt-dlp/<id>/        每個都是一個 venv；venv 不能搬移，所以更新用切換 symlink，不用 rename
//	yt-dlp/state.json   固定的版本與上一版（0600）
//	yt-dlp-venv         symlink，指向目前使用的 venv（相對路徑 yt-dlp/<id>）
//
// 沒有受管理的版本時，後備是映像內建的 /opt/yt-dlp（Dockerfile 把它放在 PATH 上）。
package ytdlp

import "path/filepath"

// CheckVideoID 是自我檢查用的影片：一部公開、長期存在的 YouTube 影片（M1 內建，不開放覆寫）。
// 能不能長期使用要在 M0 實測（見 OPS 待決問題）。
const CheckVideoID = "jNQXAC9IVRw"

// 目錄與檔案名稱。
const (
	// VersionsDirName 是 <tools> 底下放各個 venv 的目錄。
	VersionsDirName = "yt-dlp"
	// LinkName 是 <tools> 底下指向目前 venv 的 symlink；toolexec 的 Spec.ManagedVenv 用同一個名稱。
	LinkName = "yt-dlp-venv"
	// stateFileName 是 VersionsDirName 底下記錄固定版本與上一版的檔案。
	stateFileName = "state.json"
)

// ToolsDir 回傳資料目錄底下放受管理工具的目錄。
func ToolsDir(data string) string { return filepath.Join(data, "tools") }

// VersionsDir 回傳放各個 venv 的目錄。
func VersionsDir(tools string) string { return filepath.Join(tools, VersionsDirName) }

// LinkPath 回傳指向目前 venv 的 symlink 的路徑。
func LinkPath(tools string) string { return filepath.Join(tools, LinkName) }
