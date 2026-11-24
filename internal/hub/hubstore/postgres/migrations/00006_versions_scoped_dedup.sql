-- +goose Up
-- 安全修補 A：版本的鍵 (video_id, kind, variant, lang, sha256) 不再唯一（HREP 待決問題 R8「去重與可見度」）。
-- 新上傳者看不到既有版本或可見度不同時另建一列、只共用 blob；並行的分享改以鎖住影片的列序列化。
ALTER TABLE versions DROP CONSTRAINT versions_key;
CREATE INDEX versions_key ON versions (video_id, kind, variant, lang, sha256);

-- +goose Down
-- 同一個鍵已有多列時失敗（不能自動合併不同上傳者的版本），需要人工處理。
DROP INDEX versions_key;
ALTER TABLE versions ADD CONSTRAINT versions_key UNIQUE (video_id, kind, variant, lang, sha256);
