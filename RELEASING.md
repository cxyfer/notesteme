# 發布 Notesteme

這份文件是 repo 的發布約定，打版本 tag 之前先讀完。自動化在 [`.github/workflows/release.yml`](.github/workflows/release.yml)。

## 一次發布會做什麼

版本就是 `vMAJOR.MINOR.PATCH`（可加 `-rc.N` 等預發布後綴）的 git tag。image 只在推送 `v*` tag 時建置；PR 與合併到 `master` 不會產生任何發布物。推送 tag 後 workflow 依序：

1. 驗證 tag 格式（不合格式就中止）。
2. 跑完整的 [`ci.yml`](.github/workflows/ci.yml)。
3. 建置 Client 與 Hub 的 `linux/amd64`、`linux/arm64` image，只帶暫存 tag `staging-<tag>`。
4. 對推上去的 digest 跑冒煙測試（[`smoke-test.sh`](deploy/docker/smoke-test.sh)、[`smoke-test-hub.sh`](deploy/docker/smoke-test-hub.sh)），同時用 goreleaser 建置原生 binary。
5. 全部通過後把版本 tag 指向測過的 digest。
6. 建立 **GitHub Release 草稿**並附上壓縮檔與 `checksums.txt`。

版本號全部取自 tag 並寫進 binary，repo 裡沒有需要同步修改的版本檔。發布說明與公開 Release 是手動步驟。

## 發布物

| 發布物 | 內容 |
|---|---|
| `ghcr.io/cxyfer/notesteme` | Client（server 與 CLI），`linux/amd64`、`linux/arm64` |
| `ghcr.io/cxyfer/notesteme-hub` | Hub，`linux/amd64`、`linux/arm64` |
| `notesteme_<版本>_<os>_<arch>` | Client 原生 binary：Linux amd64／arm64、Windows amd64（zip）、macOS arm64 |
| `notesteme-hub_<版本>_linux_<arch>` | Hub 原生 binary：Linux amd64／arm64 |
| `checksums.txt` | 以上壓縮檔的 sha256 |

原生 binary 不含 `yt-dlp`、`ffmpeg`、`deno`，要自行安裝。

## Image tag

| Tag | 指向 |
|---|---|
| `X.Y.Z`、`X.Y.Z-rc.N` | 該版本（git tag 去掉 `v`） |
| `X.Y` | 該 minor 系列中已發布的最高正式版 |
| `latest` | 已發布的最高正式版 |

預發布版本與較舊版本的 tag 不會移動 `latest`、`X.Y`。

## 規則

1. **只對 `master` 上 CI 通過的 commit 打 tag。** workflow 檔案必須已經在那個 commit 上。
2. **不移動、不重用已發布的 tag。** 發布物有問題時發新的 patch 版本。
3. **發布前提交升級 fixture（NFR-CMP-5）。** `internal/store/migrations/` 最新的版本號 N 沒有 `internal/store/testdata/upgrade/schema-N/` 時，依[該目錄的 README](internal/store/testdata/upgrade/README.md) 產生並提交。
4. **發布說明用固定格式**：[`.github/release-notes-template.md`](.github/release-notes-template.md)，由 [`scripts/release-notes.sh`](scripts/release-notes.sh) 產生骨架。每個段落與條列只用一行原始碼（GitHub 會把 release 內文的換行當成硬換行），公開前跑 [`scripts/release-notes-lint.sh`](scripts/release-notes-lint.sh)。

## 步驟

### 1. 發布前

```bash
git switch master
git pull --ff-only
git status --short                                    # 追蹤中的檔案要沒有變更
ls internal/store/migrations | tail -n 1              # 最新的 schema 版本
ls internal/store/testdata/upgrade                    # 要有同一個版本的 schema-N/
gh run list --workflow=ci.yml --commit "$(git rev-parse HEAD)"   # 要是 success
```

### 2. 打 tag

```bash
VERSION=X.Y.Z
git tag -a "v$VERSION" -m "v$VERSION"
git push origin "v$VERSION"
gh run list --workflow=release.yml --limit 3
```

第一次試新的 pipeline 或風險較高的版本，先打 `vX.Y.Z-rc.1`。

### 3. 驗證發布物

```bash
for name in notesteme notesteme-hub; do
  docker buildx imagetools inspect "ghcr.io/cxyfer/$name:$VERSION"   # 要列出 amd64 與 arm64
  docker run --rm "ghcr.io/cxyfer/$name:$VERSION" --version          # 要印出同一個版本
done
gh release view "v$VERSION" --json isDraft,assets --jq '.isDraft, .assets[].name'
```

package 是 private 時要先 `docker login ghcr.io`（token 要有 `read:packages`）。要讓匿名使用者拉取，在兩個 package 的設定頁（*Package settings → Change visibility*）改成 Public；新的 package 第一次發布後要檢查一次。

### 4. 公開 GitHub Release

```bash
git fetch --tags origin
./scripts/release-notes.sh "v$VERSION"
$EDITOR ".tmp/release-notes-v$VERSION.md"
./scripts/release-notes-lint.sh ".tmp/release-notes-v$VERSION.md"
gh release edit "v$VERSION" --notes-file ".tmp/release-notes-v$VERSION.md" --draft=false
```

預發布版本的草稿已經標成 prerelease。

## 失敗處理

| 狀況 | 做法 |
|---|---|
| 暫時性錯誤（網路、runner） | `gh run rerun <run-id> --failed`；已發布的步驟可以重跑，草稿 Release 只更新附件 |
| 程式或 workflow 有錯，版本 tag 還沒套用（`publish` 沒有任何一個成功） | 在 `master` 修好，刪掉 tag（`git tag -d vX.Y.Z`、`git push origin :refs/tags/vX.Y.Z`），確認草稿 Release 不存在後重打 |
| 任一 image 已經套用版本 tag 但發布物有問題 | 不重用 tag；發新的 patch 版本，部署端先固定在前一版 |
| 兩個 image 只有一個 `publish` 成功 | 重跑失敗的 job；它套用的是同一次建置、測過的 digest |
| 需要前一版 | 設定 `NOTESTEME_IMAGE=ghcr.io/cxyfer/notesteme:<舊版本>` 後重建容器；資料庫已升級 schema 時要先還原 `pre-migration-<版本>.db`，舊版程式拒絕開較新的 schema |

`staging-<tag>` 是建置用的暫存 tag，和版本 tag 指向同一個 manifest；GHCR 刪除版本會一併刪掉上面所有 tag，不要單獨清它。
