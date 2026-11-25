package apierr

// 錯誤訊息目錄（NFR-I18N-2）。
//
//   - messages：固定的訊息，以繁中原文為 key（gettext 的做法），值是简中與 English。
//     handler 照舊寫繁中，回應時依語言換成對應的文字；domain 套件回傳的固定錯誤（例如「密碼不正確。」）也一樣。
//   - templates：帶參數的訊息，以名稱為 key；用 Newf 建立，參數只能是不需要翻譯的值（ID、數字、大小、scope 名稱）。
//
// 不在目錄中的訊息照原樣（繁中）回傳。目錄的涵蓋範圍由 catalog_test.go 檢查：internal/api 中直接寫成字串常值的訊息都要在目錄中。
type translation struct{ hans, en string }

var messages = map[string]translation{
	// Huma 產生的錯誤與共用的訊息（DefaultMessages、NotImplemented）
	"請求格式不正確。":       {"请求格式不正确。", "The request is malformed."},
	"找不到指定的資源。":      {"找不到指定的资源。", "The requested resource was not found."},
	"不支援這個 HTTP 方法。": {"不支持这个 HTTP 方法。", "This HTTP method is not supported."},
	"請求內容太大。":        {"请求内容太大。", "The request body is too large."},
	"不支援這種內容類型。":     {"不支持这种内容类型。", "This content type is not supported."},
	"請求內容不正確。":       {"请求内容不正确。", "The request content is invalid."},
	"伺服器發生錯誤。":       {"服务器发生错误。", "The server encountered an error."},
	"這個功能尚未實作。":      {"这个功能尚未实现。", "This feature is not implemented yet."},

	// 驗證與存取（AUTH、GRD、MCP）
	"尚未登入或登入已失效。":        {"尚未登录或登录已失效。", "You are not signed in, or your session has expired."},
	"CSRF token 缺少或不正確。": {"CSRF token 缺少或不正确。", "The CSRF token is missing or incorrect."},
	"密碼不正確。":             {"密码不正确。", "The password is incorrect."},
	"新密碼不能是空的。":          {"新密码不能为空。", "The new password cannot be empty."},
	"這個實例以 --insecure-no-auth 啟動，只接受以 localhost、127.0.0.1 或 [::1] 存取。": {
		"这个实例以 --insecure-no-auth 启动，只接受通过 localhost、127.0.0.1 或 [::1] 访问。",
		"This instance was started with --insecure-no-auth and only accepts access through localhost, 127.0.0.1 or [::1].",
	},
	"這個實例以 --insecure-no-auth 啟動，拒絕來自其他網站的請求。": {
		"这个实例以 --insecure-no-auth 启动，拒绝来自其他网站的请求。",
		"This instance was started with --insecure-no-auth and refuses requests from other sites.",
	},
	"無法連線到身分提供者，請稍後再試。": {"无法连接到身份提供者，请稍后再试。", "Cannot reach the identity provider. Try again later."},
	"MCP 不接受這個主機名稱（Host）。請把它加進設定檔的 mcp.allowed_hosts。": {
		"MCP 不接受这个主机名（Host）。请把它加入配置文件的 mcp.allowed_hosts。",
		"MCP does not accept this host name (Host). Add it to mcp.allowed_hosts in the config file.",
	},
	"MCP 不接受這個來源（Origin）。": {"MCP 不接受这个来源（Origin）。", "MCP does not accept this origin."},
	"MCP 不接受這個來源（Origin）。請把它加進設定檔的 mcp.allowed_origins。": {
		"MCP 不接受这个来源（Origin）。请把它加入配置文件的 mcp.allowed_origins。",
		"MCP does not accept this origin. Add it to mcp.allowed_origins in the config file.",
	},
	"同時處理的 MCP 請求太多，請稍後再試。": {"同时处理的 MCP 请求太多，请稍后再试。", "Too many MCP requests are being handled. Try again later."},
	"只能新增媒體資料夾中的檔案。":        {"只能添加媒体文件夹中的文件。", "Only files in the media folders can be added."},
	"只能讀取媒體資料夾中的檔案。":        {"只能读取媒体文件夹中的文件。", "Only files in the media folders can be read."},
	"這個項目只能在設定檔中定義。":        {"这个项目只能在配置文件中定义。", "This item can only be defined in the config file."},

	// 只有 owner 可以做的事（requireOwner 的各種動作；組出來的整句在這裡）
	"只有 owner 可以上傳 cookie。": {"只有 owner 可以上传 cookie。", "Only the owner can upload cookies."},
	"只有 owner 可以修改密碼。":      {"只有 owner 可以修改密码。", "Only the owner can change the password."},
	"只有 owner 可以備份。":        {"只有 owner 可以备份。", "Only the owner can make backups."},
	"只有 owner 可以刪除影音。":      {"只有 owner 可以删除媒体。", "Only the owner can delete items."},
	"只有 owner 可以回退 yt-dlp。": {"只有 owner 可以回退 yt-dlp。", "Only the owner can roll back yt-dlp."},
	"只有 owner 可以更新 yt-dlp。": {"只有 owner 可以更新 yt-dlp。", "Only the owner can update yt-dlp."},
	"只有 owner 可以查看系統資訊。":    {"只有 owner 可以查看系统信息。", "Only the owner can view system information."},
	"只有 owner 可以登出。":        {"只有 owner 可以退出登录。", "Only the owner can sign out."},

	// 找不到
	"找不到這部影音。":         {"找不到这个媒体。", "This item was not found."},
	"找不到這個產出。":         {"找不到这个产出。", "This artifact was not found."},
	"找不到這個術語候選。":       {"找不到这个术语候选。", "This term candidate was not found."},
	"找不到這個訂閱。":         {"找不到这个订阅。", "This subscription was not found."},
	"找不到這個詞彙表。":        {"找不到这个词汇表。", "This glossary was not found."},
	"找不到這個檢視。":         {"找不到这个检视。", "This saved view was not found."},
	"找不到這個路徑。":         {"找不到这个路径。", "This path was not found."},
	"找不到這個頻道。":         {"找不到这个频道。", "This channel was not found."},
	"找不到這筆單價覆寫。":       {"找不到这条单价覆盖。", "This price override was not found."},
	"找不到指定的 provider。": {"找不到指定的 provider。", "The provider was not found."},
	"style：找不到這個樣式。":   {"style：找不到这个样式。", "style: this style was not found."},
	"這部影音在垃圾桶中。":       {"这个媒体在回收站中。", "This item is in the trash."},

	// 請求內容
	"url 與 path 只能擇一提供。":           {"url 与 path 只能二选一。", "Provide either url or path, not both."},
	"請提供要新增的網址（url）或本機檔案路徑（path）。": {"请提供要添加的网址（url）或本地文件路径（path）。", "Provide the URL (url) or the local file path (path) to add."},
	"請提供要估算的網址（url）。":              {"请提供要估算的网址（url）。", "Provide the URL (url) to estimate."},
	"本機檔案還不支援估算花費。":                {"本地文件还不支持估算花费。", "Cost estimates are not supported for local files yet."},
	"這個路徑不是一般檔案。":                  {"这个路径不是普通文件。", "This path is not a regular file."},
	"這個檔案沒有音訊或影片，無法新增。":            {"这个文件没有音频或视频，无法添加。", "This file has no audio or video and cannot be added."},
	"目前有其他上傳正在進行，請稍後再試。":           {"目前有其他上传正在进行，请稍后再试。", "Another upload is in progress. Try again later."},
	"請求已取消。":                       {"请求已取消。", "The request was cancelled."},
	"請求的內容不正確。":                    {"请求的内容不正确。", "The request content is invalid."},
	"游標不正確或已失效，請從第一頁重新讀取。":         {"游标不正确或已失效，请从第一页重新读取。", "The cursor is invalid or expired. Start again from the first page."},
	"status 的值不能用逗號分隔；多個條件請重複 status 參數，例如 status=metadata:done&status=normalize:done。": {
		"status 的值不能用逗号分隔；多个条件请重复 status 参数，例如 status=metadata:done&status=normalize:done。",
		"status values cannot be comma-separated; repeat the status parameter for several conditions, for example status=metadata:done&status=normalize:done.",
	},
	"status 的格式是 <步驟>:<狀態>，例如 normalize:done。": {"status 的格式是 <步骤>:<状态>，例如 normalize:done。", "status must be <stage>:<status>, for example normalize:done."},
	"format 必須是 json 或 md。":                    {"format 必须是 json 或 md。", "format must be json or md."},
	"format 必須是 srt、vtt、txt、cues-json、ass 或 bilingual-srt。": {
		"format 必须是 srt、vtt、txt、cues-json、ass 或 bilingual-srt。",
		"format must be srt, vtt, txt, cues-json, ass or bilingual-srt.",
	},
	"字幕的 format 必須是 json、srt、vtt 或 txt。": {"字幕的 format 必须是 json、srt、vtt 或 txt。", "The subtitle format must be json, srt, vtt or txt."},
	"筆記的 format 必須是 md。":                 {"笔记的 format 必须是 md。", "The note format must be md."},
	"from 不是正確的時間。":                      {"from 不是正确的时间。", "from is not a valid time."},
	"to 不是正確的時間。":                        {"to 不是正确的时间。", "to is not a valid time."},
	"n 必須是從 1 起算的整數。":                    {"n 必须是从 1 开始的整数。", "n must be an integer starting from 1."},
	"size 必須是 full 或 thumb。":             {"size 必须是 full 或 thumb。", "size must be full or thumb."},
	"on_existing 只能是 add_versions。":      {"on_existing 只能是 add_versions。", "on_existing can only be add_versions."},
	"請上傳 bundle（multipart 的 file 欄位）。":   {"请上传 bundle（multipart 的 file 字段）。", "Upload the bundle (the file field of a multipart request)."},
	"請上傳匯入檔。":                            {"请上传导入文件。", "Upload the import file."},
	"請指定 provider 的 type。":               {"请指定 provider 的 type。", "Specify the provider type."},
	"請提供 JSON 物件格式的 config。":             {"请提供 JSON 对象格式的 config。", "Provide config as a JSON object."},
	"請提供 JSON 物件格式的內容。":                  {"请提供 JSON 对象格式的内容。", "Provide the content as a JSON object."},
	"請提供 JSON 物件格式的覆寫內容。":                {"请提供 JSON 对象格式的覆盖内容。", "Provide the overrides as a JSON object."},
	"請提供 JSON 物件格式的設定內容。":                {"请提供 JSON 对象格式的设置内容。", "Provide the settings as a JSON object."},
	"服務設定的格式不正確。":                        {"服务设置的格式不正确。", "The service settings are malformed."},
	"服務設定包含不支援的欄位或格式不正確。":                {"服务设置包含不支持的字段或格式不正确。", "The service settings contain unsupported fields or are malformed."},
	"API key 格式不正確。":                     {"API key 格式不正确。", "The API key is malformed."},
	"cookie 內容超過 64 KiB。":                {"cookie 内容超过 64 KiB。", "The cookie content exceeds 64 KiB."},

	// 功能狀態與外部服務
	"佇列尚未啟用。":                    {"队列尚未启用。", "The queue is not enabled yet."},
	"訂閱尚未啟用。":                    {"订阅尚未启用。", "Subscriptions are not enabled yet."},
	"這個 provider 不提供測試連線。":       {"这个 provider 不提供连接测试。", "This provider does not offer a connection test."},
	"測試連線失敗。":                    {"连接测试失败。", "The connection test failed."},
	"更新被中斷，沒有做任何變更。":             {"更新被中断，没有做任何更改。", "The update was interrupted; nothing was changed."},
	"yt-dlp 安裝失敗，詳見 server log。": {"yt-dlp 安装失败，详见 server log。", "Installing yt-dlp failed; see the server log."},
	"這個術語候選已經決定過了。":              {"这个术语候选已经决定过了。", "This term candidate has already been decided."},
	"尚未設定 Hub。請在設定檔加入 hub.url，並在設定頁填入 Hub token。": {
		"尚未设置 Hub。请在配置文件加入 hub.url，并在设置页填入 Hub token。",
		"Hub is not set up. Add hub.url to the config file and enter the Hub token on the settings page.",
	},

	// handler 自己的驗證（上傳、匯入檔、批次、檢視、試跑、樣式預覽、字幕匯入）
	"檢視至少要有一個篩選條件或分組。":                    {"检视至少要有一个筛选条件或分组。", "A saved view needs at least one filter or grouping."},
	"名稱不能含控制字元（例如換行、tab）。":                {"名称不能含控制字符（例如换行、tab）。", "The name cannot contain control characters (such as line breaks or tabs)."},
	"名稱不能是空的。":                            {"名称不能是空的。", "The name cannot be empty."},
	"overrides 必須是 JSON 物件。":              {"overrides 必须是 JSON 对象。", "overrides must be a JSON object."},
	"請提供要上傳的檔案（欄位 file）。":                 {"请提供要上传的文件（字段 file）。", "Provide the file to upload (the file field)."},
	"overrides 太長（上限 64 KiB）。":            {"overrides 太长（上限 64 KiB）。", "overrides is too long (the limit is 64 KiB)."},
	"一次只能上傳一個檔案（file）。":                   {"一次只能上传一个文件（file）。", "Only one file (file) can be uploaded at a time."},
	"無法判斷檔案格式，請指定 format（srt、vtt 或 ass）。": {"无法判断文件格式，请指定 format（srt、vtt 或 ass）。", "The file format cannot be determined; specify format (srt, vtt or ass)."},
	"檔案裡沒有任何字幕。":                          {"文件里没有任何字幕。", "The file has no subtitles."},
	"檔案中有過長的一行。":                          {"文件中有过长的一行。", "The file has a line that is too long."},
	"字幕檔的格式不正確。":                          {"字幕文件的格式不正确。", "The subtitle file is malformed."},
	"lang 太長。":                            {"lang 太长。", "lang is too long."},
	"LLM 請求失敗。":                           {"LLM 请求失败。", "The LLM request failed."},
	"to 必須是 HH:MM:SS 或 MM:SS（可帶小數）。":      {"to 必须是 HH:MM:SS 或 MM:SS（可带小数）。", "to must be HH:MM:SS or MM:SS (decimals allowed)."},
	"from 必須是 HH:MM:SS 或 MM:SS（可帶小數）。":    {"from 必须是 HH:MM:SS 或 MM:SS（可带小数）。", "from must be HH:MM:SS or MM:SS (decimals allowed)."},
	"無法讀取 path 指定的檔案，或超過大小上限。":            {"无法读取 path 指定的文件，或超过大小上限。", "The file given by path cannot be read, or it exceeds the size limit."},
	"檔案超過大小上限。":                           {"文件超过大小上限。", "The file exceeds the size limit."},
	"無法讀取 path 指定的檔案：找不到，或不是一般檔案。":        {"无法读取 path 指定的文件：找不到，或不是普通文件。", "The file given by path cannot be read: it was not found or is not a regular file."},
	"path 必須是絕對路徑。":                       {"path 必须是绝对路径。", "path must be an absolute path."},
	"內容超過大小上限。":                           {"内容超过大小上限。", "The content exceeds the size limit."},
	"請提供 path（伺服器上的檔案）或 content（檔案內容）。":   {"请提供 path（服务器上的文件）或 content（文件内容）。", "Provide path (a file on the server) or content (the file content)."},
	"path 與 content 只能擇一。":                {"path 与 content 只能二选一。", "Provide either path or content, not both."},
	"ids 必須有 1～100 部影音。":                  {"ids 必须有 1～100 个媒体。", "ids must have 1–100 items."},
	"一次最多選 500 部影音。":                      {"一次最多选 500 个媒体。", "At most 500 items can be selected at once."},
	"找不到處理方案 。":                           {"找不到处理方案 。", "The profile was not found."},
	"from 必須是 pipeline 的步驟。":              {"from 必须是 pipeline 的步骤。", "from must be a pipeline stage."},
	"至少要選一部影音。":                           {"至少要选一个媒体。", "Select at least one item."},
	"item：找不到這部影音。":                       {"item：找不到这个媒体。", "item: this item was not found."},
	"at：不是正確的時間。":                         {"at：不是正确的时间。", "at: not a valid time."},
	"item：請指定影音。":                         {"item：请指定媒体。", "item: specify an item."},
}

// templates 是帶參數的訊息：{0}、{1}… 依序換成參數。key 是穩定的名稱，與 code 無關。
var templates = map[string]struct{ hant, hans, en string }{
	// 欄位錯誤：{0} 是路徑（不翻譯），{1} 是說明（在目錄中時一併翻譯）。繁中與 domain 套件原本的「路徑：說明」逐字相同。
	"field_invalid":           {"{0}：{1}", "{0}：{1}", "{0}: {1}"},
	"not_ready":               {"服務尚未就緒：{0} 無法使用。", "服务尚未就绪：{0} 无法使用。", "The service is not ready: {0} unavailable."},
	"view_exists":             {"已經有名稱是「{0}」的檢視。", "已经有名称是「{0}」的检视。", "A saved view named \"{0}\" already exists."},
	"view_limit":              {"最多只能儲存 {0} 個檢視，請先刪除不用的檢視。", "最多只能保存 {0} 个检视，请先删除不用的检视。", "At most {0} saved views can be stored. Delete unused views first."},
	"search_limit":            {"limit 必須是 1～{0} 的整數。", "limit 必须是 1～{0} 的整数。", "limit must be an integer from 1 to {0}."},
	"upload_too_many_parts":   {"欄位太多（上限 {0} 個）。", "字段太多（上限 {0} 个）。", "Too many fields (the limit is {0})."},
	"upload_field_too_long":   {"{0} 太長。", "{0} 太长。", "{0} is too long."},
	"upload_until_invalid":    {"until 必須是 {0} 其中之一。", "until 必须是 {0} 其中之一。", "until must be one of {0}."},
	"upload_unknown_field":    {"不支援的欄位：{0}。", "不支持的字段：{0}。", "Unsupported field: {0}."},
	"import_path_ext":         {"path 的副檔名必須是 {0}。", "path 的扩展名必须是 {0}。", "The extension of path must be {0}."},
	"batch_unknown_action":    {"不支援的批次動作 {0}。", "不支持的批量操作 {0}。", "Unsupported batch action {0}."},
	"batch_profile_not_found": {"找不到處理方案 {0}。", "找不到处理方案 {0}。", "Profile {0} was not found."},
	// Huma 產生的驗證錯誤：{0} 是位置（body.name、query.limit），{1} 是原因（huma.go 的 humaReasons）。
	"huma_invalid_at":   {"請求內容不正確：{0} {1}。", "请求内容不正确：{0} {1}。", "The request content is invalid: {0} {1}."},
	"huma_malformed_at": {"請求格式不正確：{0} {1}。", "请求格式不正确：{0} {1}。", "The request is malformed: {0} {1}."},
	"forbidden_scope":   {"權限不足：需要 {0} scope。", "权限不足：需要 {0} scope。", "Permission denied: the {0} scope is required."},
	"proxy_user_not_allowed": {
		"這個帳號（{0}）不能使用 Notesteme。", "这个账号（{0}）不能使用 Notesteme。", "This account ({0}) is not allowed to use Notesteme.",
	},
	"unsupported_search_mode": {"不支援這種搜尋方式：{0}。", "不支持这种搜索方式：{0}。", "This search mode is not supported: {0}."},
	"param_rfc3339": {
		"{0} 必須是 RFC 3339 時間，例如 2026-01-02T15:04:05Z。", "{0} 必须是 RFC 3339 时间，例如 2026-01-02T15:04:05Z。",
		"{0} must be an RFC 3339 time, for example 2026-01-02T15:04:05Z.",
	},
	"param_nonneg_seconds": {"{0} 必須是不小於 0 的整數（秒）。", "{0} 必须是不小于 0 的整数（秒）。", "{0} must be a non-negative integer (seconds)."},
	"upload_too_large":     {"上傳的檔案超過大小上限（{0}）。", "上传的文件超过大小上限（{0}）。", "The uploaded file exceeds the size limit ({0})."},
	"bundle_too_large":     {"bundle 超過 {0} 的上傳上限。", "bundle 超过 {0} 的上传上限。", "The bundle exceeds the upload limit of {0}."},
	"artifact_not_readable": {
		"這種產出（{0}）不能讀取內容，只有字幕與筆記可以。", "这种产出（{0}）不能读取内容，只有字幕与笔记可以。",
		"The content of this kind of artifact ({0}) cannot be read; only subtitles and notes can.",
	},
	// CLI 與 serve 的版本不相容（CLI-R9）：與 internal/compat.Message 的文字相同。
	"cli_too_old": {
		"CLI 版本太舊（CLI {0}，serve {1}），請升級 CLI。", "CLI 版本太旧（CLI {0}，serve {1}），请升级 CLI。",
		"The CLI is too old (CLI {0}, serve {1}). Upgrade the CLI.",
	},
	"serve_too_old": {
		"serve 版本太舊（serve {1}，CLI {0}），請升級 serve，或換成 {2} 版的 CLI。", "serve 版本太旧（serve {1}，CLI {0}），请升级 serve，或换成 {2} 版的 CLI。",
		"serve is too old (serve {1}, CLI {0}). Upgrade serve, or use a {2} CLI.",
	},
	"glossary_full": {
		"詞彙表已有 {0} 個詞，請先刪除不用的詞再接受新的術語。", "词汇表已有 {0} 个词，请先删除不用的词再接受新的术语。",
		"The glossary already has {0} terms. Delete unused terms before accepting new ones.",
	},
}
