# ADR-0003：時間戳只取自來源，LLM 不輸出時間

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，新增句群翻譯、章節與筆記的時間來源、`split_timing_audio`，`Cue` 預留 `Speaker`

## 背景

- 偏好使用轉錄模型或字幕檔給出的時間戳。
- 各來源的時間粒度不同：
  - word 級：`whisper-1`、YouTube 自動字幕（json3）、WhisperX。
  - segment 級：Mistral Voxtral 3。
  - cue 級：人工字幕、Bilibili 字幕、Podcast transcript。
  - chunk 級：Supadata。
- 如果讓 LLM 斷句時順便輸出時間，常會出現漂移、重疊或憑空編造的時間。

## 決策

1. **LLM 只回傳文字和斷點標記，時間一律由程式計算。** 章節與筆記也一樣，LLM 只引用 cue 編號。
2. 各種操作的時間來源：

   | 操作 | 時間來源 |
   |---|---|
   | 校正、翻譯（一對一） | 原 cue 的時間，不變 |
   | 合併相鄰 cue | 第一句的 start 到最後一句的 end |
   | 段內切分，有 word 級時間 | 斷點所在字詞的 end |
   | 段內切分，沒有 word 級時間 | 依 `split_timing` 策略處理 |
   | 句群翻譯（一句跨越多個 cue） | LLM 在譯文中插入切分標記，程式依標記把譯文分回原本的 cue，時間不變；標記數不符時退回逐 cue 翻譯 |
   | 章節起點、筆記中的時間連結 | LLM 引用的 cue 的 start，程式檢查編號確實存在 |

3. `split_timing` 策略：
   - `forbid`：不允許段內切分，時間 100% 來自來源。
   - `when_needed`（預設）：只有超過長度或時長上限時才切。先用 ffmpeg `silencedetect` 在段內找停頓點，找不到才依字數比例插值。
   - `free`：讓 LLM 自由斷句，時間的處理方式和 `when_needed` 相同。
   - `silencedetect` 需要音訊，是否為此下載由 `split_timing_audio` 控制：`auto` 在需要段內切分、又沒有 word 級時間時只下載音訊；`never` 直接依字數插值。`wsl` profile 預設 `auto`，`vps` profile 預設 `never`，因為在 VPS 上下載音訊一樣會觸發 YouTube 反爬。
4. 每個 cue 記錄時間怎麼來的：

   ```go
   type TimingOrigin string // source | merged | silence | interpolated | model

   type Cue struct {
       Start, End time.Duration
       Text       string
       Words      []Word       // 可為空
       Timing     TimingOrigin
       Speaker    string       // 可為空，對應 Transcript.Speakers 的 id；說話者辨識之後再做，格式先預留
   }
   ```

5. 人工字幕的斷句通常已經很好，預設跳過 `segment`，只做 `correct` 和 `translate`。
6. `normalize` 步驟與匯出前都要檢查時間：單調遞增、重疊不超過容許值、句子之間保留最小間隔。

## 考慮過的選項

- **讓 LLM 輸出時間戳**：不可靠，而且違反「時間只取自來源」的偏好。
- **一律做 forced alignment**（例如 wav2vec2）：要下載音訊並在 Python 環境跑模型，太重。保留給本地 ASR adapter 選用。
- **segment 級完全不允許切分**：Voxtral 這類來源的 segment 可能太長，不適合當字幕顯示，所以保留 `when_needed` 作為預設。

## 影響

- 好處：
  - 時間精度取決於來源本身，結果可預期。
  - UI 可以標示哪些 cue 的時間是推算出來的。
  - Hub 上的使用者可以依時間來源篩選。
- 代價：
  - 需要實作「斷點標記對回字詞與時間」的對齊程式（LLM 可能改動文字，要用模糊比對），並寫完整的 golden 測試。句群翻譯的切分標記也一樣需要 golden 測試。
- M0 要用實際樣本量測各來源的 segment 長度分佈，用來調整 `when_needed` 的門檻；同時量測斷點標記對齊的成功率。
