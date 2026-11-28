# 發布說明的固定格式

每個 GitHub Release 都用這個格式，流程見 [RELEASING.md](../RELEASING.md)。

```bash
./scripts/release-notes.sh vX.Y.Z          # 寫出 .tmp/release-notes-vX.Y.Z.md
$EDITOR .tmp/release-notes-vX.Y.Z.md       # 填完 TODO
./scripts/release-notes-lint.sh .tmp/release-notes-vX.Y.Z.md
```

## 版型

```markdown
> [!IMPORTANT]
> One paragraph on what this release is and why it matters.
>
> What changes for an existing install: migrations that run at startup, defaults that moved, anything to do before or after upgrading. Delete this paragraph when there is nothing to say.

## Highlights

* **Area**: What changed, in plain user-facing prose, and what it means for the reader.

---

## What's Changed
* <generated; the first release has no such section>

**Full Changelog**: <generated>
```

## 規則

1. **發布說明用英文撰寫。** repo 的其他文件維持中文；`What's Changed` 由 commit 與 PR 標題產生，照原文保留。
2. **先寫提示框。** 多數讀者只看到這裡：版本是什麼，接著是會壞掉或改變的事。預發布版本要寫明並指出確切的 tag。
3. **Highlights 寫給使用者看。** 一個領域一條，相關的 commit 合併描述，不貼 commit subject。
4. **預設值改變要附解法。**
5. **已知限制照實寫。** 沒達標或沒驗收的項目不省略。
6. **內文不手動換行。** 一個段落或條列一行。
