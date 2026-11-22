# OpenCC 字典與設定

這個目錄的檔案原樣取自 OpenCC（https://github.com/BYVoid/OpenCC）`ver.1.4.2` release 的 `opencc-v1.4.2-resources.zip`，
授權是 Apache License 2.0（見同目錄的 `LICENSE`）。沒有修改任何內容。

- 來源 commit：`025f371dc76b598d77384fbdab90c937471844d8`（見 `manifest.json`）
- resources zip 的 sha256：`9ea0d303219b34d014d5c116677b5d325043beafb2c8a62ee889ca67f4d054a5`
- 只保留 s2t、s2tw、s2twp、s2hk、t2s、tw2s、tw2sp、hk2s、t2tw、t2hk 十份設定與它們引用的 17 份字典；`manifest.json` 是上游清單中這些檔案的條目，`TestManifest` 會逐一比對 sha256。
- `../testdata/testcases.json` 取自同一個 tag 的 `test/testcases/testcases.json`（sha256 `3f8273c5f432090ec1a8a01bd4d545046ad5a28e69eb41a081c5e3e54d3e402b`），上游檔案有多餘的逗號，讀取時要寬鬆解析。
