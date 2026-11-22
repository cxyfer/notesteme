@M5
Feature: Hub accounts, permissions and takedowns
  As a hub member, I want to sign in with Google, GitHub or Linux.do and link several of them to one account,
  so that I am not locked to one provider.
  As a hub admin, I want to set who can see what and handle takedown requests,
  so that the Hub stays within the rules.

  Background:
    Given a Hub is running

  # --- 登入方式（HACC-R1～R5） ---

  @HUB-01 @HACC-R1
  Scenario: Only configured providers are offered
    Given the Hub config contains:
      """
      oauth:
        google: { client_id: "" }
      """
    When the Hub client sends "GET /api/v1/auth/providers"
    Then the response status is 200
    And the response contains:
      """
      { "providers": [ { "id": "github" }, { "id": "linuxdo" } ] }
      """

  @HUB-01 @HACC-R1
  Scenario: A provider that is not configured cannot start a sign-in
    Given the Hub config contains:
      """
      oauth:
        google: { client_id: "" }
      """
    When "alice" starts signing in to the Hub with "google"
    Then the response status is 404
    And the response contains:
      """
      { "code": "provider_not_configured", "message": "這個登入方式尚未啟用。" }
      """

  @HUB-01 @HACC-R2
  Scenario: Starting a sign-in redirects with a state and a PKCE challenge
    When "alice" starts signing in to the Hub with "github"
    Then the response status is 302
    And the response header "Location" contains "https://github.com/login/oauth/authorize?"
    And the response header "Location" contains "state="
    And the response header "Location" contains "code_challenge_method=S256"
    And the response header "Location" contains "redirect_uri=https%3A%2F%2Fhub.example.org%2Fauth%2Fgithub%2Fcallback"
    And the response header "Set-Cookie" contains "notesteme_hub_oauth="
    And the response header "Set-Cookie" contains "HttpOnly"

  @HUB-01 @HACC-R2
  Scenario: Google sign-ins also carry a nonce
    When "alice" starts signing in to the Hub with "google"
    Then the response status is 302
    And the response header "Location" contains "nonce="
    And the response header "Location" contains "scope=openid"

  @HUB-01 @HACC-R2 @HACC-R3
  Scenario: The token request proves the PKCE verifier
    When "alice" signs in to the Hub with "linuxdo"
    Then the response status is 302
    And the OAuth provider "linuxdo" received a valid PKCE verifier

  @HUB-01 @HACC-R3
  Scenario: A callback with a different state is rejected
    When "alice" starts signing in to the Hub with "github"
    And the pending sign-in returns with a different state
    Then the response status is 400
    And the response contains:
      """
      { "code": "oauth_state_invalid", "message": "登入請求已過期或無效，請重新登入。" }
      """
    And the number of Hub accounts is 0

  @HUB-01 @HACC-R3
  Scenario: A sign-in callback cannot be replayed
    When "alice" signs in to the Hub with "github"
    And the last sign-in callback is replayed
    Then the response status is 400
    And the response contains:
      """
      { "code": "oauth_state_invalid" }
      """

  @HUB-01 @HACC-R3
  Scenario: A sign-in that is not finished within ten minutes expires
    When "alice" starts signing in to the Hub with "github"
    And the clock advances by "11m"
    And the OAuth provider "github" completes the pending sign-in
    Then the response status is 400
    And the response contains:
      """
      { "code": "oauth_state_invalid" }
      """

  @HUB-01 @HACC-R3
  Scenario: Cancelling at the provider is explained
    Given the OAuth provider "github" denies the next sign-in
    When "alice" signs in to the Hub with "github"
    Then the response status is 400
    And the response contains:
      """
      { "code": "oauth_denied", "message": "你取消了登入。" }
      """
    And the number of Hub accounts is 0

  @HUB-01 @HACC-R4 @HACC-R10
  Scenario: The first sign-in creates a member account
    When "alice" signs in to the Hub with "github"
    Then the response status is 302
    And the response header "Set-Cookie" contains "notesteme_hub_session="
    When the hub member "alice" sends "GET /api/v1/me"
    Then the response status is 200
    And the response contains:
      """
      {
        "display_name": "alice",
        "identities": [ { "provider": "github", "login": "alice" } ],
        "groups": [ "members" ],
        "permissions": [ "read", "download" ]
      }
      """

  @HUB-01 @HACC-R4
  Scenario: A renamed provider account still signs in to the same Hub account
    Given the hub member "alice" signed in with "github"
    And the OAuth provider "github" has the user "alice" with:
      | field | value     |
      | login | alice-new |
    When "alice" signs in to the Hub with "github"
    Then the response status is 302
    And the number of Hub accounts is 1

  @HUB-01 @HACC-R5
  Scenario: The same email on another provider does not merge accounts
    Given the hub member "alice" signed in with "github"
    When "alice" signs in to the Hub with "google"
    Then the response status is 302
    And the number of Hub accounts is 2

  # --- 連結與移除登入方式（HACC-R6、R7） ---

  @HUB-01 @HACC-R6
  Scenario: A signed-in member links another provider to the same account
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" links the "google" user "alice"
    Then the response status is 302
    When "alice" signs in to the Hub with "google"
    Then the number of Hub accounts is 1
    When the hub member "alice" sends "GET /api/v1/me"
    Then the response contains:
      """
      { "identities": [ { "provider": "github" }, { "provider": "google" } ] }
      """

  @HUB-01 @HACC-R6
  Scenario: An identity that belongs to another account cannot be linked
    Given the hub member "alice" signed in with "github"
    And the hub member "bob" signed in with "linuxdo"
    When the hub member "bob" links the "github" user "alice"
    Then the response status is 409
    And the response contains:
      """
      { "code": "identity_in_use", "message": "這個 GitHub 帳號已連結到另一個 Hub 帳號。" }
      """
    And the number of Hub accounts is 2

  @HUB-01 @HACC-R6
  Scenario: A second account of the same provider cannot be linked
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" links the "github" user "alice-work"
    Then the response status is 409
    And the response contains:
      """
      { "code": "provider_already_linked", "message": "這個帳號已經連結了一個 GitHub 帳號。" }
      """

  @HUB-01 @HACC-R6
  Scenario: Linking needs a recent sign-in
    Given the hub member "alice" signed in with "github"
    When the clock advances by "11m"
    And the hub member "alice" links the "google" user "alice"
    Then the response status is 403
    And the response contains:
      """
      { "code": "reauth_required", "message": "請重新登入後再連結新的登入方式。" }
      """

  @HUB-01 @HACC-R7
  Scenario: The last sign-in method cannot be removed
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" sends "DELETE /api/v1/me/identities/github"
    Then the response status is 409
    And the response contains:
      """
      { "code": "last_identity", "message": "這是你唯一的登入方式，不能移除。" }
      """

  @HUB-01 @HACC-R7
  Scenario: A linked sign-in method can be removed
    Given the hub member "alice" signed in with "github"
    And the hub member "alice" links the "google" user "alice"
    When the hub member "alice" sends "DELETE /api/v1/me/identities/google"
    Then the response status is 204
    When "alice" signs in to the Hub with "google"
    Then the number of Hub accounts is 2

  # --- session 與 PAT（HACC-R8、R9） ---

  @HUB-01 @HACC-R8
  Scenario: Session requests that change data need a CSRF token
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" sends "POST /api/v1/me/tokens" without a CSRF token
    Then the response status is 403
    And the response contains:
      """
      { "code": "csrf_invalid" }
      """

  @HUB-01 @HACC-R8
  Scenario: Signing out ends the session at once
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" sends "POST /api/v1/auth/logout"
    And the hub member "alice" sends "GET /api/v1/me"
    Then the response status is 401

  @HUB-07 @HACC-R9
  Scenario: A Hub token works with the permissions it was given
    Given the hub member "alice" signed in with "github"
    And a Hub token "reader" for "alice" with the permissions "read"
    When the Hub client sends "GET /api/v1/me" with the Hub token "reader"
    Then the response status is 200
    When the Hub client sends "GET /api/v1/versions/00000000-0000-0000-0000-000000000000/download?format=srt" with the Hub token "reader"
    Then the response status is 403
    And the response contains:
      """
      { "code": "forbidden", "required_permission": "download" }
      """

  @HUB-07 @HACC-R9
  Scenario: A token cannot carry a permission the account does not have
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" sends "POST /api/v1/me/tokens" with:
      """
      { "name": "uploader", "permissions": ["read", "upload"] }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "permission_not_held", "errors": [ { "path": "permissions.1" } ] }
      """

  @HUB-07 @HACC-R9
  Scenario: Token lifetimes are limited
    Given the hub member "alice" signed in with "github"
    When the hub member "alice" sends "POST /api/v1/me/tokens" with:
      """
      { "name": "forever", "permissions": ["read"], "expires_in_days": 400 }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "expires_in_days" } ] }
      """

  @HUB-07 @HACC-R9
  Scenario: An expired Hub token stops working
    Given the hub member "alice" signed in with "github"
    And a Hub token "reader" for "alice" with the permissions "read"
    When the clock advances by "2161h"
    And the Hub client sends "GET /api/v1/me" with the Hub token "reader"
    Then the response status is 401

  @HUB-07 @HACC-R9 @HACC-R11
  Scenario: A token loses what its account loses
    Given the Hub config contains:
      """
      groups:
        contributors:
          rules:
            - { provider: linuxdo, trust_level_gte: 2 }
      """
    And the OAuth provider "linuxdo" has the user "alice" with:
      | field       | value |
      | trust_level | 2     |
    And the hub member "alice" signed in with "linuxdo"
    And a Hub token "up" for "alice" with the permissions "upload"
    And the OAuth provider "linuxdo" has the user "alice" with:
      | field       | value |
      | trust_level | 1     |
    When "alice" signs in to the Hub with "linuxdo"
    And the Hub client sends "POST /api/v1/shares" with the Hub token "up"
    Then the response status is 403
    And the response contains:
      """
      { "required_permission": "upload" }
      """

  # --- 群組與自動規則（HACC-R10～R12） ---

  @HUB-07 @HACC-R10 @HACC-R11
  Scenario: A Linux.do trust level grants the contributor permissions
    Given the Hub config contains:
      """
      groups:
        contributors:
          rules:
            - { provider: linuxdo, trust_level_gte: 2 }
      """
    And the OAuth provider "linuxdo" has the user "alice" with:
      | field       | value |
      | trust_level | 3     |
    And the hub member "alice" signed in with "linuxdo"
    When the hub member "alice" sends "POST /api/v1/me/tokens" with:
      """
      { "name": "uploader", "permissions": ["upload", "regenerate"] }
      """
    Then the response status is 201

  @HUB-07 @HACC-R11
  Scenario: An email domain rule needs a verified email
    Given the Hub config contains:
      """
      groups:
        contributors:
          rules:
            - { provider: google, email_domain: [example.com] }
      """
    And the OAuth provider "google" has the user "carol" with:
      | field          | value             |
      | email          | carol@example.com |
      | email_verified | false             |
    And the hub member "carol" signed in with "google"
    When the hub member "carol" sends "POST /api/v1/me/tokens" with:
      """
      { "name": "uploader", "permissions": ["upload"] }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "permission_not_held" }
      """

  @HUB-07 @HACC-R11
  Scenario: Manual group assignments are not touched by the rules
    Given the Hub config contains:
      """
      groups:
        contributors:
          rules:
            - { provider: github, org_member: [notesteme-friends] }
      """
    And the hub member "alice" signed in with "github"
    And the hub member "alice" is in the group "contributors"
    When "alice" signs in to the Hub with "github"
    And the hub member "alice" sends "POST /api/v1/me/tokens" with:
      """
      { "name": "uploader", "permissions": ["upload"] }
      """
    Then the response status is 201

  @HUB-07 @HACC-R12
  Scenario: The first admin comes from the config file
    Given the Hub config contains:
      """
      admins:
        - { provider: github, subject: github-root }
      """
    And the hub member "root" signed in with "github"
    When the hub member "root" sends "GET /api/v1/admin/audit"
    Then the response status is 200

  @HUB-07 @HACC-R12
  Scenario: Admins cannot be appointed through the API
    Given the hub member "root" signed in with "github"
    And the hub member "root" is in the group "admins"
    And the hub member "bob" signed in with "github"
    When the hub member "root" sends "PUT /api/v1/admin/users/{user:bob}/groups/admins"
    Then the response status is 403
    And the response contains:
      """
      { "code": "admin_by_config", "message": "管理員只能在設定檔或用 notesteme-hub admin grant 指定。" }
      """

  @HUB-07 @HACC-R12 @HACC-R13
  Scenario: An admin appoints a moderator
    Given the hub member "root" signed in with "github"
    And the hub member "root" is in the group "admins"
    And the hub member "bob" signed in with "github"
    When the hub member "bob" sends "GET /api/v1/admin/takedowns"
    Then the response status is 403
    And the response contains:
      """
      { "required_permission": "moderate" }
      """
    When the hub member "root" sends "PUT /api/v1/admin/users/{user:bob}/groups/moderators"
    Then the response status is 204
    When the hub member "bob" sends "GET /api/v1/admin/takedowns"
    Then the response status is 200

  # --- 權限與可見度（HACC-R13、R14） ---

  @HUB-07 @HACC-R13
  Scenario: Nothing but sign-in is open without an account by default
    Given the Hub has the version "raw" of "yt:hacc01xxxxx" with:
      | field      | value  |
      | visibility | public |
    When the Hub client sends "GET /api/v1/versions"
    Then the response status is 401
    And the response contains:
      """
      { "code": "unauthorized", "message": "請先登入 Hub。" }
      """

  @HUB-07 @HACC-R13 @HACC-R14
  Scenario: A public site lets visitors read public versions only
    Given the Hub config contains:
      """
      site: { visibility: public }
      """
    And the Hub has the version "open" of "yt:hacc01xxxxx" with:
      | field      | value  |
      | visibility | public |
    And the Hub has the version "members" of "yt:hacc02xxxxx" with:
      | field      | value         |
      | visibility | authenticated |
    When the Hub client sends "GET /api/v1/versions/{version:open}"
    Then the response status is 200
    When the Hub client sends "GET /api/v1/versions/{version:members}"
    Then the response status is 404
    When the Hub client sends "GET /api/v1/versions/{version:open}/download?format=srt"
    Then the response status is 401

  @HUB-07 @HACC-R14
  Scenario: Private versions look the same as missing ones to others
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "mine" of "yt:hacc01xxxxx" with:
      | field      | value   |
      | uploader   | alice   |
      | visibility | private |
    When the hub member "bob" sends "GET /api/v1/versions/{version:mine}"
    Then the response status is 404
    And the response contains:
      """
      { "code": "not_found", "message": "找不到這個版本。" }
      """

  @HUB-07 @HACC-R14
  Scenario: Group versions are visible to the group only
    Given the Hub config contains:
      """
      groups:
        translators: { permissions: [read, download] }
      """
    And the hub member "bob" signed in with "github"
    And the hub member "carol" signed in with "github"
    And the hub member "carol" is in the group "translators"
    And the Hub has the version "team" of "yt:hacc01xxxxx" with:
      | field      | value             |
      | visibility | group:translators |
    When the hub member "bob" sends "GET /api/v1/versions/{version:team}"
    Then the response status is 404
    When the hub member "carol" sends "GET /api/v1/versions/{version:team}"
    Then the response status is 200

  @HUB-07 @HACC-R14
  Scenario: Frames are private unless set otherwise
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "shots" of "yt:hacc01xxxxx" with:
      | field    | value  |
      | kind     | frames |
      | variant  |        |
      | uploader | alice  |
    When the hub member "bob" sends "GET /api/v1/versions/{version:shots}"
    Then the response status is 404

  # --- 下架（HACC-R15、R16） ---

  @HUB-07 @HACC-R15
  Scenario: Anyone can request a takedown without an account
    Given the Hub has the version "raw" of "yt:hacc01xxxxx" with:
      | field    | value |
      | uploader | alice |
    When the Hub client sends "POST /api/v1/takedowns" with:
      """
      {
        "version_id": "{version:raw}",
        "reason": "copyright",
        "statement": "I own the copyright of this video and its subtitles.",
        "contact_email": "rights@example.com",
        "good_faith": true
      }
      """
    Then the response status is 201
    And the response contains:
      """
      { "state": "open" }
      """

  @HUB-07 @HACC-R15
  Scenario: A takedown request needs a real statement and a good-faith declaration
    Given the Hub has the version "raw" of "yt:hacc01xxxxx" with:
      | field    | value |
      | uploader | alice |
    When the Hub client sends "POST /api/v1/takedowns" with:
      """
      {
        "version_id": "{version:raw}",
        "reason": "copyright",
        "statement": "mine",
        "contact_email": "rights@example.com",
        "good_faith": false
      }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "statement" }, { "path": "good_faith" } ] }
      """

  @HUB-07 @HACC-R15
  Scenario: Takedown requests are rate limited per address
    Given the Hub config contains:
      """
      rate_limits: { takedowns_per_hour: 1 }
      """
    And the Hub has the version "raw" of "yt:hacc01xxxxx" with:
      | field    | value |
      | uploader | alice |
    When the Hub client sends "POST /api/v1/takedowns" with:
      """
      { "version_id": "{version:raw}", "reason": "other", "statement": "This version is not what it claims to be.", "contact_email": "a@example.com", "good_faith": true }
      """
    And the Hub client sends "POST /api/v1/takedowns" with:
      """
      { "version_id": "{version:raw}", "reason": "other", "statement": "This version is not what it claims to be.", "contact_email": "a@example.com", "good_faith": true }
      """
    Then the response status is 429
    And the response contains:
      """
      { "code": "rate_limited" }
      """

  @HUB-07 @HACC-R16
  Scenario: A moderator removes a version after a takedown request
    Given the hub member "mod" signed in with "github"
    And the hub member "mod" is in the group "moderators"
    And the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hacc01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the Hub client sends "POST /api/v1/takedowns" with:
      """
      { "version_id": "{version:raw}", "reason": "copyright", "statement": "I own the copyright of this video and its subtitles.", "contact_email": "rights@example.com", "good_faith": true }
      """
    When the hub member "mod" sends "POST /api/v1/admin/takedowns/{takedown:last}/decision" with:
      """
      { "decision": "remove", "note": "Confirmed with the rights holder." }
      """
    Then the response status is 200
    And the Hub version "raw" is "removed"
    When the hub member "bob" sends "GET /api/v1/versions/{version:raw}"
    Then the response status is 410
    And the response contains:
      """
      { "code": "removed", "message": "這個版本已依下架申請移除。" }
      """

  @HUB-07 @HACC-R16
  Scenario: A rejected takedown leaves the version alone and cannot be decided twice
    Given the hub member "mod" signed in with "github"
    And the hub member "mod" is in the group "moderators"
    And the Hub has the version "raw" of "yt:hacc01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the Hub client sends "POST /api/v1/takedowns" with:
      """
      { "version_id": "{version:raw}", "reason": "other", "statement": "This version is not what it claims to be.", "contact_email": "a@example.com", "good_faith": true }
      """
    When the hub member "mod" sends "POST /api/v1/admin/takedowns/{takedown:last}/decision" with:
      """
      { "decision": "reject", "note": "Not a valid claim." }
      """
    Then the response status is 200
    And the Hub version "raw" is "visible"
    When the hub member "mod" sends "POST /api/v1/admin/takedowns/{takedown:last}/decision" with:
      """
      { "decision": "remove", "note": "Changed my mind." }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "takedown_decided" }
      """

  # --- 停用、稽核與限流（HACC-R17、R18） ---

  @HUB-07 @HACC-R17
  Scenario: A suspended account loses its sessions and tokens
    Given the hub member "root" signed in with "github"
    And the hub member "root" is in the group "admins"
    And the hub member "bob" signed in with "github"
    And a Hub token "reader" for "bob" with the permissions "read"
    When the hub member "root" sends "POST /api/v1/admin/users/{user:bob}/suspend" with:
      """
      { "reason": "spam uploads" }
      """
    Then the response status is 200
    When the hub member "bob" sends "GET /api/v1/me"
    Then the response status is 403
    And the response contains:
      """
      { "code": "account_suspended", "message": "這個帳號已被停用。" }
      """
    When the Hub client sends "GET /api/v1/me" with the Hub token "reader"
    Then the response status is 403

  @HUB-07 @HACC-R17
  Scenario: Admin actions are written to the audit log
    Given the hub member "root" signed in with "github"
    And the hub member "root" is in the group "admins"
    And the hub member "bob" signed in with "github"
    And the hub member "root" sends "POST /api/v1/admin/users/{user:bob}/suspend" with:
      """
      { "reason": "spam uploads" }
      """
    When the hub member "root" sends "GET /api/v1/admin/audit"
    Then the response status is 200
    And the response contains:
      """
      { "entries": [ { "actor": "root", "action": "user.suspend", "target_kind": "user" } ] }
      """

  @HUB-07 @HACC-R18
  Scenario: Too many requests are slowed down
    Given the Hub config contains:
      """
      rate_limits: { requests_per_minute: 2 }
      """
    And the hub member "alice" signed in with "github"
    When the hub member "alice" sends "GET /api/v1/me"
    And the hub member "alice" sends "GET /api/v1/me"
    And the hub member "alice" sends "GET /api/v1/me"
    Then the response status is 429
    And the response contains:
      """
      { "code": "rate_limited", "retry_after_s": "*" }
      """

  # --- Hub UI ---

  @HUB-01 @HUB-07 @ui @manual
  Scenario: The sign-in, account and admin pages of the Hub
    Given a Hub with Google, GitHub and Linux.do configured
    When a visitor opens the Hub
    Then the sign-in page lists the three providers
    When the visitor signs in with GitHub and opens the account page
    Then they can link Google, remove a linked provider, and create and revoke Hub tokens
    And an admin can assign groups, suspend accounts and decide takedown requests on the admin pages
