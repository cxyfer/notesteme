# ADR-0003：時間戳只取自來源，LLM 不輸出時間

- 狀態：已採納
- 日期：2026-11-11

## 背景

- 偏好使用轉錄模型或字幕檔給出的時間戳。
- 各來源的時間粒度不同：
  - word 級：`whisper-1`、YouTube 自動字幕（json3）、WhisperX。
  - segment 級：Mistral Voxtral 3。
  - cue 級：人工字幕、Bilibili 字幕、Podcast transcript。
  - chunk 級：Supadata。
- 如果讓 LLM 斷句時順便輸出時間，常會出現漂移、重疊或憑空編造的時間。

## 決策

1. **LLM 只回傳文字和斷點標記，時間一律由程式計算。**
2. 各種操作的時間來源：

   | 操作 | 時間來源 |
   |---|---|
   | 校正、翻譯（一對一） | 原 cue 的時間，不變 |
   | 合併相鄰 cue | 第一句的 start 到最後一句的 end |
   | 段內切分，有 word 級時間 | 斷點所在字詞的 end |
   | 段內切分，沒有 word 級時間 | 依 `split_timing` 策略處理 |

3. `split_timing` 策略：
   - `forbid`：不允許段內切分，時間 100% 來自來源。
   - `when_needed`（預設）：只有超過長度或時長上限時才切。先用 ffmpeg `silencedetect` 在段內找停頓點，找不到才依字數比例插值。
   - `free`：讓 LLM 自由斷句，時間的處理方式和 `when_needed` 相同。
4. 每個 cue 記錄時間怎麼來的：

   ```go
   type TimingOrigin string // source | merged | silence | interpolated | model

   type Cue struct {
       Start, End time.Duration
       Text       string
       Words      []Word       // 可為空
       Timing     TimingOrigin
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
  - 需要實作「斷點標記對回字詞與時間」的對齊程式（LLM 可能改動文字，要用模糊比對），並寫完整的 golden 測試。
- M0 要用實際樣本量測各來源的 segment 長度分佈，用來調整 `when_needed` 的門檻。
