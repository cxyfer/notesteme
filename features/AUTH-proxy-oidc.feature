@M4
Feature: Reverse proxy sign-in, OIDC, sessions and token management
  As the owner, I want to sign in through my reverse proxy or my identity provider,
  see and revoke my sessions, and manage tokens without the CLI.

  # 反向代理驗證（AUTH-06）

  @AUTH-06 @AUTH-R12
  Scenario: Proxy sign-in needs a list of allowed users
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    When the server starts
    Then the server fails to start with "設定了 auth.proxy.user_header，但 trusted_proxies 或 auth.proxy.allowed_users 是空的。"

  @AUTH-06 @AUTH-R12
  Scenario: The proxy header cannot reuse a header with another meaning
    Given the reverse proxy at "127.0.0.5" signs users in with the header "X-Forwarded-For"
    And the reverse proxy allows the users "alice"
    When the server starts
    Then the server fails to start with "auth.proxy.user_header 不能使用 X-Forwarded-For。"

  @AUTH-06 @AUTH-R13 @AUTH-R14
  Scenario: A trusted proxy signs in an allowed user
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    And the reverse proxy allows the users "alice"
    When the reverse proxy at "127.0.0.5" forwards "GET /api/items" with the header "Remote-User" set to "alice"
    Then the response status is 200
    And the response header "Set-Cookie" contains "notesteme_session="
    And the response header "Set-Cookie" contains "notesteme_csrf="

  @AUTH-06 @AUTH-R13
  Scenario: The header from an untrusted address is ignored and logged
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    And the reverse proxy allows the users "alice"
    When the reverse proxy at "127.0.0.9" forwards "GET /api/items" with the header "Remote-User" set to "eve-untrusted-7731"
    Then the response status is 401
    And the auth log contains "忽略來自不受信任位址 127.0.0.9 的 Remote-User 標頭。"
    And the logs do not contain "eve-untrusted-7731"

  @AUTH-06 @AUTH-R14
  Scenario: A user the proxy knows but Notesteme does not is refused
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    And the reverse proxy allows the users "alice"
    When the reverse proxy at "127.0.0.5" forwards "GET /api/items" with the header "Remote-User" set to "mallory"
    Then the response status is 403
    And the response contains:
      """
      { "code": "proxy_user_not_allowed", "message": "這個帳號（mallory）不能使用 Notesteme。" }
      """

  @AUTH-06 @AUTH-R15
  Scenario: Proxy sign-in still needs a CSRF token for changes
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    And the reverse proxy allows the users "alice"
    When the reverse proxy at "127.0.0.5" forwards "GET /api/items" with the header "Remote-User" set to "alice"
    And the reverse proxy at "127.0.0.5" forwards "POST /api/queue/pause" with the header "Remote-User" set to "alice"
    Then the response status is 403
    When the reverse proxy at "127.0.0.5" forwards "POST /api/queue/pause" with the header "Remote-User" set to "alice" and the CSRF token
    Then the response status is 200

  @AUTH-06 @AUTH-R15 @mcp
  Scenario: The MCP endpoint does not accept the proxy header
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    And the reverse proxy allows the users "alice"
    When the reverse proxy at "127.0.0.5" forwards "POST /mcp" with the header "Remote-User" set to "alice"
    Then the response status is 401

  @AUTH-06 @AUTH-R15
  Scenario: The proxy header does not widen a bearer token
    Given the reverse proxy at "127.0.0.5" signs users in with the header "Remote-User"
    And the reverse proxy allows the users "alice"
    And a token "reader" with scopes "read"
    When the reverse proxy at "127.0.0.5" forwards "POST /api/items" with the token "reader" and the header "Remote-User" set to "alice"
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "ingest" }
      """

  # OIDC（AUTH-07）

  @AUTH-07 @AUTH-R16
  Scenario: OIDC routes do not exist unless OIDC is configured
    When the OIDC sign-in starts
    Then the response status is 404

  @AUTH-07 @AUTH-R16
  Scenario: The client secret must come from the environment
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    And the environment variable "NOTESTEME_OIDC_CLIENT_SECRET" is not set
    When the server starts
    Then the server fails to start with "已設定 auth.oidc，但沒有設定環境變數 NOTESTEME_OIDC_CLIENT_SECRET。"

  @AUTH-07 @AUTH-R17
  Scenario: Starting the sign-in sends the browser to the provider with PKCE
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    When the OIDC sign-in starts
    Then the response status is 302
    And the OIDC authorization request has the parameter "response_type" set to "code"
    And the OIDC authorization request has the parameter "response_mode" set to "query"
    And the OIDC authorization request has the parameter "scope" set to "openid"
    And the OIDC authorization request has the parameter "code_challenge_method" set to "S256"
    And the OIDC authorization request has the parameter "code_challenge" set to "*"
    And the OIDC authorization request has the parameter "state" set to "*"
    And the OIDC authorization request has the parameter "nonce" set to "*"
    And the response header "Set-Cookie" contains "notesteme_oidc="
    And the response header "Set-Cookie" contains "SameSite=Lax"

  @AUTH-07 @AUTH-R18 @AUTH-R19
  Scenario: An allowed subject gets a session and lands on the page it asked for
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    When the OIDC sign-in starts with the next page "/library?view=trash"
    And the OIDC sign-in is completed
    Then the OIDC sign-in redirects to "/library?view=trash"
    And the response header "Set-Cookie" contains "notesteme_session="
    And the OIDC token request used PKCE with "S256"
    When the OIDC session sends "GET /api/auth/sessions"
    Then the response status is 200
    And the response contains:
      """
      { "sessions": [ { "method": "oidc", "current": true } ] }
      """
    And the logs do not contain "fake-oidc-client-secret-7c1e"

  @AUTH-07 @AUTH-R17
  Scenario Outline: Only relative next pages are kept
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    When the OIDC sign-in starts with the next page "<next>"
    And the OIDC sign-in is completed
    Then the OIDC sign-in redirects to "<landing>"

    Examples:
      | next                      | landing          |
      | /items/abc?t=12           | /items/abc?t=12  |
      | //evil.example/phish      | /                |
      | /\\evil.example/phish     | /                |
      | https://evil.example/     | /                |
      | javascript:alert(1)       | /                |
      | library                   | /                |

  @AUTH-07 @AUTH-R18
  Scenario: A callback with another state is refused
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    When the OIDC sign-in starts
    And the OIDC sign-in is completed with the state "forged-state-value"
    Then the response status is 400
    And the response contains:
      """
      { "code": "oidc_state_invalid", "message": "登入請求已失效，請重新登入。" }
      """

  @AUTH-07 @AUTH-R18
  Scenario: A callback cannot be replayed
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    When the OIDC sign-in starts
    And the OIDC sign-in is completed
    And the OIDC callback is replayed
    Then the response status is 400
    And the response contains:
      """
      { "code": "oidc_state_invalid" }
      """

  @AUTH-07 @AUTH-R18
  Scenario Outline: ID tokens that fail verification are refused
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    And the OIDC provider issues ID tokens with "<fault>"
    When the OIDC sign-in starts
    And the OIDC sign-in is completed
    Then the response status is 401
    And the response contains:
      """
      { "code": "oidc_failed", "message": "無法驗證身分提供者的回應，請重新登入。" }
      """
    And the OIDC callback set no session cookie

    Examples:
      | fault                     |
      | a bad signature           |
      | an unknown key            |
      | no signature              |
      | an HMAC signature         |
      | the wrong issuer          |
      | the wrong audience        |
      | the wrong nonce           |
      | an expiry 120s ago        |
      | an issue time 120s ahead  |

  @AUTH-07 @AUTH-R18
  Scenario Outline: Small clock differences are tolerated
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    And the OIDC provider issues ID tokens with "<fault>"
    When the OIDC sign-in starts
    And the OIDC sign-in is completed
    Then the OIDC sign-in redirects to "/"

    Examples:
      | fault                    |
      | an expiry 30s ago        |
      | an issue time 30s ahead  |

  @AUTH-07 @AUTH-R18
  Scenario: An error from the provider is shown without echoing arbitrary text
    Given the OIDC provider "https://id.example.com" signs in the subject "user-7f3a"
    And the OIDC provider allows the subjects "user-7f3a"
    And the OIDC provider denies the sign-in with "access_denied"
    When the OIDC sign-in starts
    And the OIDC sign-in is completed
    Then the response status is 401
    And the response contains:
      """
      { "code": "oidc_failed", "message": "身分提供者拒絕了登入（access_denied）。" }
      """

  @AUTH-07 @AUTH-R19
  Scenario: A subject that is not allowed gets no session
    Given the OIDC provider "https://id.example.com" signs in the subject "someone-else"
    And the OIDC provider allows the subjects "user-7f3a"
    When the OIDC sign-in starts
    And the OIDC sign-in is completed
    Then the response status is 403
    And the response contains:
      """
      { "code": "oidc_not_allowed", "message": "這個帳號不能使用 Notesteme。" }
      """
    And the OIDC callback set no session cookie

  # 工作階段與 token（AUTH-08）

  @AUTH-08 @AUTH-R20
  Scenario: Sessions are listed without their secrets
    Given the session "laptop" is signed in from "127.0.0.4"
    When the session "laptop" sends "GET /api/auth/sessions"
    Then the response status is 200
    And the response contains:
      """
      { "sessions": [ { "id": "*", "method": "password", "ip": "127.0.0.4", "current": true, "last_seen_at": "*" } ] }
      """
    And the session list does not contain the secret of the session "laptop"

  @AUTH-08 @AUTH-R20
  Scenario: Revoking a session signs it out immediately
    Given the session "laptop" is signed in from "127.0.0.4"
    And the owner is signed in
    When the session "laptop" is revoked by the owner
    Then the response status is 204
    When the session "laptop" sends "GET /api/items"
    Then the response status is 401
    When the owner sends "GET /api/items"
    Then the response status is 200

  @AUTH-08 @AUTH-R20
  Scenario: Revoking the current session is a sign-out
    Given the session "phone" is signed in from "127.0.0.6"
    When the session "phone" sends "DELETE /api/auth/sessions/current"
    Then the response status is 204
    And the response header "Set-Cookie" contains "notesteme_session=;"
    When the session "phone" sends "GET /api/items"
    Then the response status is 401

  @AUTH-08 @AUTH-R20
  Scenario: Listing sessions needs the admin scope
    Given the session "laptop" is signed in from "127.0.0.4"
    And a token "reader" with scopes "read"
    When the client sends "GET /api/auth/sessions" with the token "reader"
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """

  @AUTH-08 @AUTH-R21
  Scenario: Rotating a token invalidates the old secret at once
    Given a token "agent1" with scopes "read,agent"
    When the token "agent1" is rotated as "agent1-new"
    Then the response status is 200
    And the response contains:
      """
      { "name": "agent1", "scopes": ["read", "agent"], "token": "*" }
      """
    When the client sends "GET /api/items" with the token "agent1"
    Then the response status is 401
    When the client sends "GET /api/items" with the token "agent1-new"
    Then the response status is 200

  @AUTH-08 @AUTH-R22 @ui @manual
  Scenario: Tokens are created, rotated and revoked in the settings page
    Given the owner is on the "MCP 與 Agent" section of the settings
    When the owner creates a token named "claude" with the scopes "read, agent" and the expiry "30 天"
    Then the full token is shown once with a copy button, and the list shows its prefix, scopes and expiry
    When the owner chooses "輪替" for "claude" and confirms "輪替後，舊的 token 會立即失效，請更新使用它的程式。"
    Then a new token is shown once and the old one no longer works
    When the owner chooses "撤銷" for "claude" and confirms "撤銷後，使用這個 token 的程式會立即失去存取權。"
    Then the list shows "claude" as revoked

  @AUTH-07 @AUTH-08 @AUTH-R20 @AUTH-R22 @ui @manual
  Scenario: The sign-in page offers OIDC and the settings page lists sessions
    Given OIDC is configured with the issuer "https://id.example.com"
    When the owner opens the sign-in page
    Then there is a "用 id.example.com 登入" button next to the password field
    When the owner signs in with OIDC and opens the "登入" section of the settings
    Then the current session is marked and every other session has a "撤銷" button
