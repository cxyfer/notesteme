Feature: Sign-in and tokens
  As the owner, I want every instance protected by a password and tokens that
  only carry the permissions they need.

  @M1 @AUTH-01 @AUTH-R1
  Scenario Outline: Unauthenticated requests are refused
    Given the server is running
    When the client sends "<request>"
    Then the response status is <status>

    Examples:
      | request                | status |
      | GET /api/items         | 401    |
      | POST /api/items        | 401    |
      | POST /mcp              | 401    |
      | GET /healthz           | 200    |
      | GET /readyz            | 200    |

  @M1 @AUTH-01 @AUTH-R2
  Scenario: The first start takes the password from the environment
    Given the environment variable "NOTESTEME_PASSWORD" is "correct horse"
    When the server starts
    And someone signs in with the password "correct horse"
    Then the response status is 200
    And the data directory does not contain "correct horse"

  @M1 @AUTH-01 @AUTH-R3
  Scenario: The server refuses to start without any password
    Given the environment variable "NOTESTEME_PASSWORD" is not set
    When the server starts
    Then the server fails to start with "尚未設定登入密碼。請設定 NOTESTEME_PASSWORD 後重新啟動。"

  @M1 @AUTH-01 @AUTH-R4
  Scenario: The session cookie is locked down
    Given the owner password is "correct horse"
    And the server is running
    When someone signs in with the password "correct horse"
    Then the response header "Set-Cookie" contains "HttpOnly"
    And the response header "Set-Cookie" contains "SameSite=Lax"

  @M1 @AUTH-01 @AUTH-R4
  Scenario: Signing out ends the session
    Given the server is running
    And the owner is signed in
    When the owner sends "POST /api/auth/logout"
    And the owner sends "GET /api/items"
    Then the response status is 401

  @M1 @AUTH-01 @AUTH-R5
  Scenario: A state-changing request without a CSRF token is refused
    Given the server is running
    And the owner is signed in
    When the owner sends "POST /api/queue/pause" without a CSRF token
    Then the response status is 403

  @M1 @AUTH-03 @AUTH-R5
  Scenario: Bearer tokens do not need a CSRF token
    Given the server is running
    And a token "cli" with scopes "ingest"
    When the client sends "POST /api/items" with the token "cli" and:
      """
      { "url": "https://www.youtube.com/watch?v=abc123" }
      """
    Then the response status is 201

  @M1 @AUTH-02 @AUTH-R6
  Scenario: Five wrong passwords lock sign-in for a minute
    Given the owner password is "correct horse"
    And the clock is at "2026-11-12T10:00:00Z"
    And the server is running
    When someone signs in with the password "wrong 1"
    And someone signs in with the password "wrong 2"
    And someone signs in with the password "wrong 3"
    And someone signs in with the password "wrong 4"
    And someone signs in with the password "wrong 5"
    And someone signs in with the password "correct horse"
    Then the response status is 429
    And the response contains:
      """
      { "message": "密碼不正確。", "retry_after_s": 60 }
      """
    When the clock advances by "61s"
    And someone signs in with the password "correct horse"
    Then the response status is 200

  @M1 @AUTH-02 @AUTH-R6
  Scenario: A lockout from one address does not lock out another
    Given the owner password is "correct horse"
    And the server is running
    When someone signs in from "127.0.0.2" with the password "wrong 1"
    And someone signs in from "127.0.0.2" with the password "wrong 2"
    And someone signs in from "127.0.0.2" with the password "wrong 3"
    And someone signs in from "127.0.0.2" with the password "wrong 4"
    And someone signs in from "127.0.0.2" with the password "wrong 5"
    And someone signs in from "127.0.0.2" with the password "correct horse"
    Then the response status is 429
    When someone signs in from "127.0.0.3" with the password "correct horse"
    Then the response status is 200

  @M1 @AUTH-03 @AUTH-R7 @AUTH-R10 @cli
  Scenario: A token is shown once and stored only as a hash
    When the owner runs "notesteme tokens create --name agent --scopes read,agent --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "name": "agent", "scopes": ["read", "agent"] }
      """
    And the data directory does not contain the created token

  @M1 @AUTH-03 @AUTH-R8
  Scenario Outline: Each scope only opens its own doors
    Given the server is running
    And a token "t" with scopes "<scopes>"
    When the client sends "<request>" with the token "t"
    Then the response status is <status>

    Examples:
      | scopes     | request                       | status |
      | read       | GET /api/items                | 200    |
      | read       | POST /api/items               | 403    |
      | read,agent | POST /api/queue/pause         | 403    |
      | ingest     | POST /api/items               | 201    |
      | ingest     | PUT /api/settings/services    | 403    |
      | admin      | PUT /api/settings/services    | 200    |

  @M1 @AUTH-03 @AUTH-R8
  Scenario: A missing scope is named in the error
    Given the server is running
    And a token "reader" with scopes "read"
    When the client sends "POST /api/items" with the token "reader"
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "ingest" }
      """

  @M1 @AUTH-04 @AUTH-R9
  Scenario: A revoked token stops working immediately
    Given the server is running
    And the owner is signed in
    And a token "old" with scopes "read"
    When the owner sends "DELETE /api/tokens/old"
    And the client sends "GET /api/items" with the token "old"
    Then the response status is 401

  @M1 @AUTH-04 @AUTH-R9
  Scenario: An expired token stops working
    Given the server is running
    And a token "temp" with scopes "read"
    And the token "temp" expired
    When the client sends "GET /api/items" with the token "temp"
    Then the response status is 401

  @M2 @AUTH-05 @AUTH-R11 @mcp
  Scenario: MCP requests from an unexpected origin are refused
    Given the server is running
    And a token "agent" with scopes "read,agent"
    When the client sends "POST /mcp" with the token "agent" from the origin "https://evil.example.com"
    Then the response status is 403
