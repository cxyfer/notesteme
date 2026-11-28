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
> 一段話說明這個版本是什麼、為什麼重要。
>
> 現有安裝要注意的事：啟動時執行的 migration、改變的預設值、升級前後要做的事。沒有就刪掉這段。

## Highlights

* **領域**：用使用者看得懂的話說明改了什麼、對他代表什麼。

---

## What's Changed
* <自動產生；第一個版本沒有這一節>

**Full Changelog**: <自動產生>
```

## 規則

1. **先寫提示框。** 多數讀者只看到這裡：版本是什麼，接著是會壞掉或改變的事。預發布版本要寫明並指出確切的 tag。
2. **Highlights 寫給使用者看。** 一個領域一條，相關的 commit 合併描述，不貼 commit subject。
3. **預設值改變要附解法。**
4. **已知限制照實寫。** 沒達標或沒驗收的項目不省略。
5. **內文不手動換行。** 一個段落或條列一行。
