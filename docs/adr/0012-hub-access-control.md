# ADR-0012：Hub 權限：可見度、群組與自動規則

- 狀態：已採納（細節由 [ADR-0031](0031-hub-identity-permissions-and-content.md) 定案，2026-11-23，待 owner 追認）
- 日期：2026-11-11

## 背景

- Hub 的權限由管理員設定。預設只有登入者能使用，也可以依信任等級或群組調整。
- 登入方式有 Google（OIDC）、GitHub（OAuth2，不是 OIDC）、Linux.do Connect。
- Linux.do Connect 是標準的 OAuth2 授權碼流程：
  - authorize：`https://connect.linux.do/oauth2/authorize`
  - token：`https://connect.linux.do/oauth2/token`
  - 使用者資訊：`https://connect.linux.do/api/user`（回傳 `trust_level`，範圍 0–4）

## 決策

### 身分

- `users` 與 `identities` 是一對多：一個帳號可以綁定多個登入方式。
- 用各 provider 不會變動的 ID 作為識別：Linux.do 用 `id`，GitHub 用數字 ID，Google 用 `sub`。不用使用者名稱。

### 權限模型

| 層 | 設計 |
|---|---|
| 站台預設可見度 | 管理員設定，預設 `authenticated`（需登入） |
| 群組 | 管理員手動指派，加上自動規則 |
| 權限 | `read`、`download`、`upload`、`regenerate`、`moderate`、`admin` |
| 單一內容的可見度 | `public`、`authenticated`、`group:<id>`、`private`；不能超過站台的上限 |

### 自動規則

```yaml
groups:
  contributors:
    permissions: [read, download, upload, regenerate]
    rules:
      - provider: linuxdo
        trust_level_gte: 2
      - provider: github
        org_member: [my-org]        # 需要 read:org scope
      - provider: google
        email_domain: [example.com]
```

- 每次登入時重新評估規則，因為 trust level 和 org 成員資格都可能變動。
- 被管理員手動指派的群組不受規則影響。

### 內容類型

- 可以為不同的產出類型設定不同的預設可見度。例如畫面預設 `private`，字幕預設跟隨站台設定。

### API

- Client 和 MCP 用 PAT（personal access token）存取 Hub。PAT 可以限制權限範圍，也可以設到期時間。
- 之後再評估是否實作完整的 MCP OAuth 授權流程。

## 考慮過的選項

- **只分「公開」與「需登入」兩種**：無法依信任等級限制上傳，容易被濫用。
- **完整的 ABAC 政策引擎**：目前不需要這麼複雜，上面的規則已經足夠。

## 影響

- 好處：
  - 可以依社群的信任機制（例如 Linux.do trust level）控制誰能上傳。
  - 降低被濫用的風險。
- 代價：
  - 要為每個 provider 實作屬性擷取。GitHub 的 org 成員資格需要額外的 scope 和 API 呼叫。
