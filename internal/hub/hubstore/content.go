package hubstore

import (
	"context"
	"errors"
	"time"
)

// 內容倉庫（HREP，M5-S3）：影片、blob、版本、分享與衍生關係（ADR-0031 第 3、5 點）。

// ErrRemovedContent 表示 (video, sha256) 在 removed_hashes 中（HREP-R9）。
var ErrRemovedContent = errors.New("removed content")

// 衍生關係（HREP-R6）。
const (
	RelationParent          = "parent"
	RelationRegeneratedFrom = "regenerated_from"
)

// Video 是一部影片：(Platform, ExternalID, Part) 識別，沒有分P時 Part 是 0。
type Video struct {
	ID                string
	Platform          string
	ExternalID        string
	Part              int
	Title             string
	ChannelExternalID string
	ChannelTitle      string
	DurationS         *float64
	OriginalLang      string
	PublishedAt       string
	// KeySHA256 是 sha256("<platform>:<external_id>:<part>")（ADR-0032 第 3 點，ASR 前查詢用）。
	KeySHA256     string
	FirstSharedAt time.Time
}

// Version 是一份產出內容（鍵 (video, kind, variant, lang, sha256)）。
type Version struct {
	ID              string
	VideoID         string
	Kind            string
	Variant         string
	Lang            string
	Format          string
	SHA256          string
	Size            int64
	Provenance      []byte // 白名單過濾後的 JSON
	TranscribeModel string
	CorrectModel    string
	TranslateModel  string
	ModelVerified   bool
	Visibility      string
	License         string
	State           string
	Score           int
	CreatedAt       time.Time
}

// Blob 是要和版本一起記錄的檔案（內容已寫進 hubblob）。
type Blob struct {
	SHA256    string
	Size      int64
	MediaType string
}

// NewVersion 是分享中的一個產出。
type NewVersion struct {
	// Version 的 ID、VideoID、CreatedAt 由 store 填入；State 空字串時是 visible。
	Version Version
	// Images 是屬於這個版本的圖片 blob（role image）。
	Images []string
	// ParentsSHA256 是來源紀錄的 parents_sha256：同一部影片中有這些內容的版本時記為 parent。
	ParentsSHA256 []string
	// RegeneratedFrom 是 hub_origin.version_id（呼叫端已確認存在且上傳者看得到），空字串代表沒有。
	RegeneratedFrom string
}

// NewShare 是一次分享。
type NewShare struct {
	UserID       string
	Video        Video // ID、FirstSharedAt、KeySHA256 由 store 填入；影片已存在時只用來比對
	License      string
	BundleSHA256 string
	BundleSize   int64
	Blobs        []Blob
	Versions     []NewVersion
	// CanJoin 回報上傳者以一般成員身分（不靠上傳者或 moderate）看不看得到這個可見度的版本；nil 時只有
	// public 與 authenticated 看得到。去重只併入看得到、可見度相同的既有版本（ChooseExisting）。
	CanJoin func(visibility string) bool
}

// ExistingVersion 是鍵相同的既有版本；IsUploader 表示新上傳者已有它未撤回的分享。
type ExistingVersion struct {
	Version
	IsUploader bool
}

// ChooseExisting 決定分享的一個產出怎麼處理鍵相同的既有版本（HREP-R5、R8，安全修補 A）。cands 依建立順序。
// 回傳 join（併入的版本索引）或 restore（以新分享的值恢復的 withdrawn 版本索引），都是 -1 時建立新版本、只共用 blob：
//  1. 上傳者自己的版本（未撤回）直接併入（冪等）；
//  2. 否則併入 visible 或 removed、可見度與這次指定的值完全相同、且上傳者不靠上傳者身分就看得到的版本
//     （不併入別人的 private、看不到的 group、hidden，也不把指定 private 的人併進公開的版本）；
//  3. 否則恢復最早的 withdrawn 版本（撤回後再分享相同內容不會被吞掉）。
func ChooseExisting(cands []ExistingVersion, requested string, canJoin func(string) bool) (join, restore int) {
	if canJoin == nil {
		canJoin = func(v string) bool { return v == "public" || v == "authenticated" }
	}
	join, restore = -1, -1
	for i, c := range cands {
		if c.IsUploader && c.State != "withdrawn" {
			return i, -1
		}
	}
	for i, c := range cands {
		if (c.State == "visible" || c.State == "removed") && c.Visibility == requested && canJoin(requested) {
			return i, -1
		}
	}
	for i, c := range cands {
		if c.State == "withdrawn" {
			return -1, i
		}
	}
	return -1, -1
}

// SharedVersion 是分享結果中的一個版本。
type SharedVersion struct {
	Version
	Deduplicated bool
}

// ShareResult 是 CreateShare 的結果；Existing 為 true 時是同一個人已經分享過的同一個 bundle（冪等，HREP-R5）。
type ShareResult struct {
	ShareID  string
	Video    Video
	Versions []SharedVersion
	Existing bool
}

// Uploader 是版本的一位上傳者（有未撤回的分享）。
type Uploader struct {
	UserID      string
	DisplayName string
}

// Derived 是衍生關係另一端的版本。
type Derived struct {
	Version
	Relation string
}

// ShareUsage 是帳號在一段時間內的分享（HREP-R11）。
type ShareUsage struct {
	Count  int
	Bytes  int64
	Oldest time.Time // 這段時間內最早的分享；Count 為 0 時是零值
}

// ContentStore 是內容倉庫的方法（嵌入 Store）。
type ContentStore interface {
	// CreateShare 在一個交易中建立分享：影片（第一次時）、blob 紀錄、版本（鍵已存在時連到既有版本，Deduplicated）、
	// share_versions 與衍生關係（新版本的 parent、regenerated_from，以及已有版本的 parents_sha256 指到新版本時補上的 parent）。
	// 任何一個版本的 (video, sha256) 在 removed_hashes 中時回 ErrRemovedContent，什麼都不寫。
	// 同一個人已有相同 bundle_sha256、未撤回的分享時，不寫入任何東西，回傳那次分享（Existing）。
	CreateShare(ctx context.Context, in NewShare) (ShareResult, error)
	// WithdrawShare 撤回帳號自己的分享（HREP-R10）：沒有其他未撤回分享的版本變成 withdrawn（removed 不變）。
	// 不存在、屬於別人或已撤回時回 ErrNotFound。
	WithdrawShare(ctx context.Context, userID, shareID string) error
	// ShareUsage 回傳帳號在 since 之後建立的分享數與 bundle 大小合計（含已撤回的）。
	ShareUsage(ctx context.Context, userID string, since time.Time) (ShareUsage, error)

	// Version 回傳版本；不存在時回 ErrNotFound。
	Version(ctx context.Context, id string) (Version, error)
	// VideoByID 回傳影片；不存在時回 ErrNotFound。
	VideoByID(ctx context.Context, id string) (Video, error)
	// VideoByKey 以 (platform, external_id, part) 查影片；不存在時回 ErrNotFound。
	VideoByKey(ctx context.Context, platform, externalID string, part int) (Video, error)
	// VideoVersions 回傳影片的所有版本（任何狀態），依建立順序。
	VideoVersions(ctx context.Context, videoID string) ([]Version, error)
	// VersionUploaders 依第一次分享的先後回傳版本的上傳者（有未撤回分享的帳號）。
	VersionUploaders(ctx context.Context, versionID string) ([]Uploader, error)
	// VersionDerivations 回傳版本的 parents 與 children（任何狀態）。
	VersionDerivations(ctx context.Context, versionID string) (parents, children []Derived, err error)
	// BlobRefs 回傳引用這個 blob 的版本數。
	BlobRefs(ctx context.Context, sha string) (int, error)

	// SetVersionState 改變版本的狀態（下架、隱藏；驗收的 seeding）；不存在時回 ErrNotFound。
	SetVersionState(ctx context.Context, versionID, state string) error
	// AddRemovedHash 把 (video, sha256) 記入 removed_hashes（HREP-R9）。
	AddRemovedHash(ctx context.Context, videoID, sha, takedownID string) error
	// ReplaceVersionContent 換掉版本的內容 blob（驗收的 `the Hub version … has the cues:`）；不存在時回 ErrNotFound。
	ReplaceVersionContent(ctx context.Context, versionID string, b Blob) error
}
