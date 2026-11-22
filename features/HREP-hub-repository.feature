@M5
Feature: Hub repository, filters and community signals
  As the owner, I want to share my corrected subtitles and notes to the Hub with their provenance,
  so that others can reuse my work.
  As a hub member, I want to filter shared subtitles by platform, channel, language and models,
  and to vote on and report shared versions, so that I find a version I trust.

  Background:
    Given a Hub is running
    And the clock is at "2026-12-01T00:00:00Z"
    And the hub member "alice" signed in with "github"
    And the hub member "alice" is in the group "contributors"

  # --- 分享與 bundle 驗證（HREP-R1～R4） ---

  @HUB-02 @HREP-R1
  Scenario: Sharing a bundle creates one version per artifact
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant    | lang | provider | model       | parents   |
      | transcript | source     | en   | groq     | whisper-v3  |           |
      | transcript | normalized | en   |          |             | source:en |
      | transcript | corrected  | en   | openai   | gpt-4.1     | normalized:en |
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 201
    And the response contains:
      """
      {
        "video": { "platform": "youtube", "external_id": "hrep01xxxxx" },
        "versions": [
          { "kind": "transcript", "variant": "source",     "lang": "en", "deduplicated": false },
          { "kind": "transcript", "variant": "normalized", "lang": "en", "deduplicated": false },
          { "kind": "transcript", "variant": "corrected",  "lang": "en", "deduplicated": false }
        ]
      }
      """
    And the Hub has 3 versions of "yt:hrep01xxxxx"

  @HUB-02 @HREP-R1
  Scenario: Sharing needs the uploader to confirm the right to share
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    When the hub member "alice" shares the bundle "talk" with:
      | field  | value    |
      | attest | <absent> |
    Then the response status is 422
    And the response contains:
      """
      { "code": "attest_required", "message": "分享前要確認你有權分享這些內容。" }
      """
    And the Hub has 0 versions of "yt:hrep01xxxxx"

  @HUB-02 @HREP-R1
  Scenario: Members without the upload permission cannot share
    Given the hub member "bob" signed in with "github"
    And a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    When the hub member "bob" shares the bundle "talk"
    Then the response status is 403
    And the response contains:
      """
      { "required_permission": "upload" }
      """

  @HUB-02 @HREP-R2
  Scenario Outline: Unsafe bundles are rejected and leave nothing behind
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the bundle "talk" is made unsafe with "<problem>"
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid" }
      """
    And the Hub has 0 versions of "yt:hrep01xxxxx"

    Examples:
      | problem                  |
      | an absolute path         |
      | a parent directory entry |
      | a symlink                |
      | a sha256 mismatch        |

  @HUB-02 @HREP-R2
  Scenario: Bundles over the upload limit are refused
    Given the Hub config contains:
      """
      bundles: { max_upload_bytes: "1KiB" }
      """
    And a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 413
    And the response contains:
      """
      { "code": "bundle_too_large" }
      """

  @HUB-02 @HREP-R3
  Scenario Outline: Content that may not be shared is refused by the Hub too
    Given a bundle "talk" of "<ref>" with the artifacts:
      | kind       | variant | lang | source   | cookie_required   |
      | transcript | source  | en   | <source> | <cookie_required> |
    And the bundle "talk" has the manifest fields:
      | field             | value          |
      | item.availability | <availability> |
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 422
    And the response contains:
      """
      { "code": "not_shareable", "message": "<message>" }
      """

    Examples:
      | ref                  | source         | cookie_required | availability    | message                            |
      | yt:hrep02xxxxx       | youtube-manual | false           | subscriber_only | 非公開影音不能分享到 Hub。         |
      | local:lecture.mp4    | local-sidecar  | false           | public          | 本機檔案不能分享到 Hub。           |
      | yt:hrep03xxxxx       | youtube-auto   | true            | public          | 需要登入才能取得的字幕不能分享到 Hub。 |
      | bili:BV1hr4y1c7mD/p1 | bilibili-ai    | <absent>        | public          | 需要登入才能取得的字幕不能分享到 Hub。 |

  @HUB-02 @HREP-R3
  Scenario: Media files are never accepted
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the bundle "talk" is made unsafe with "a media file"
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 422
    And the response contains:
      """
      { "code": "not_shareable", "message": "bundle 中不能包含媒體檔。" }
      """

  @HUB-02 @HREP-R3
  Scenario: Only the listed licences are accepted
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the bundle "talk" has the manifest fields:
      | field   | value       |
      | license | proprietary |
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 422
    And the response contains:
      """
      { "code": "license_invalid", "message": "不支援的授權：proprietary。" }
      """

  @HUB-02 @HREP-R4
  Scenario: Provenance fields outside the allowlist are dropped
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | provider | model      |
      | transcript | source  | en   | groq     | whisper-v3 |
    And the bundle "talk" has the manifest fields:
      | field                           | value                     |
      | artifacts.0.provenance.workdir  | /data/tmp/task-42         |
      | created_by.instance_id          | 0193a3b4-0000-7000-8000-000000000001 |
    And the hub member "alice" shares the bundle "talk"
    And the Hub version of "yt:hrep01xxxxx" for "transcript:source:en" is known as "raw"
    When the hub member "alice" sends "GET /api/v1/versions/{version:raw}"
    Then the response status is 200
    And the response contains:
      """
      { "provenance": { "provider": "groq", "model": "whisper-v3" } }
      """
    And the response does not contain "/data/tmp/task-42"
    And the response does not contain "0193a3b4-0000-7000-8000-000000000001"

  @HUB-02 @HREP-R4
  Scenario: Oversized provenance is rejected
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the bundle "talk" has the manifest fields:
      | field                        | value                |
      | artifacts.0.provenance.model | <repeat:x:17408> |
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid" }
      """

  # --- 去重與衍生關係（HREP-R5、R6） ---

  @HUB-02 @HREP-R5
  Scenario: The same content shared by two members is stored once
    Given the hub member "bob" signed in with "github"
    And the hub member "bob" is in the group "contributors"
    And a bundle "a" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | content |
      | transcript | source  | en   | raw-1   |
    And a bundle "b" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | content |
      | transcript | source  | en   | raw-1   |
    And the hub member "alice" shares the bundle "a"
    When the hub member "bob" shares the bundle "b"
    Then the response status is 201
    And the response contains:
      """
      { "versions": [ { "variant": "source", "deduplicated": true } ] }
      """
    And the Hub has 1 version of "yt:hrep01xxxxx"
    And the Hub stores the content "raw-1" once
    Given the Hub version of "yt:hrep01xxxxx" for "transcript:source:en" is known as "raw"
    When the hub member "bob" sends "GET /api/v1/versions/{version:raw}"
    Then the response contains:
      """
      { "uploaders": [ { "display_name": "alice" }, { "display_name": "bob" } ] }
      """

  @HUB-02 @HREP-R5
  Scenario: Sharing the same bundle again is harmless
    Given a bundle "a" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | content |
      | transcript | source  | en   | raw-1   |
    And the hub member "alice" shares the bundle "a"
    When the hub member "alice" shares the bundle "a"
    Then the response status is 201
    And the Hub has 1 version of "yt:hrep01xxxxx"

  @HUB-02 @HREP-R6
  Scenario: Parents in the same video are linked
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant    | lang | parents       |
      | transcript | normalized | en   |               |
      | transcript | corrected  | en   | normalized:en |
    And the hub member "alice" shares the bundle "talk"
    And the Hub version of "yt:hrep01xxxxx" for "transcript:normalized:en" is known as "norm"
    And the Hub version of "yt:hrep01xxxxx" for "transcript:corrected:en" is known as "fixed"
    Then the Hub version "fixed" is derived from "norm" as "parent"

  @HUB-02 @HUB-04 @HREP-R6
  Scenario: A version regenerated from someone else's subtitles credits them
    Given the hub member "bob" signed in with "github"
    And the hub member "bob" is in the group "contributors"
    And the Hub has the version "alice-raw" of "yt:hrep01xxxxx" with:
      | field    | value  |
      | variant  | source |
      | uploader | alice  |
    And a bundle "redo" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant   | lang | executor | hub_origin |
      | transcript | corrected | en   | api      | alice-raw  |
    And the hub member "bob" shares the bundle "redo"
    And the Hub version of "yt:hrep01xxxxx" for "transcript:corrected:en" is known as "bob-fixed"
    Then the Hub version "bob-fixed" is derived from "alice-raw" as "regenerated_from"
    When the hub member "bob" sends "GET /api/v1/versions/{version:bob-fixed}"
    Then the response contains:
      """
      { "parents": [ { "relation": "regenerated_from", "summary": "以 @alice 的原始字幕重新生成" } ] }
      """

  # --- 模型、可見度、下架後的內容、撤回（HREP-R7～R10） ---

  @HUB-03 @HREP-R7
  Scenario: Versions with agent-reported models are flagged
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant   | lang | executor | model           | model_verified |
      | transcript | corrected | en   | agent    | claude-sonnet-x | false          |
    And the hub member "alice" shares the bundle "talk"
    When the hub member "alice" sends "GET /api/v1/versions?video=youtube:hrep01xxxxx"
    Then the response status is 200
    And the response contains:
      """
      { "versions": [ { "correct_model": "claude-sonnet-x", "model_verified": false, "badges": ["unverified_model"] } ] }
      """

  @HUB-02 @HREP-R8
  Scenario: The requested visibility cannot exceed the site limit
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    When the hub member "alice" shares the bundle "talk" with:
      | field      | value  |
      | visibility | public |
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "visibility", "message": "可見度不能超過站台上限（authenticated）。" } ] }
      """

  @HUB-02 @HREP-R8
  Scenario: Shared frames stay private
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
      | frames     |         |      |
    When the hub member "alice" shares the bundle "talk" with:
      | field      | value         |
      | visibility | authenticated |
    Then the response status is 201
    And the response contains:
      """
      { "versions": [ { "kind": "transcript", "visibility": "authenticated" }, { "kind": "frames", "visibility": "private" } ] }
      """

  @HUB-02 @HREP-R9
  Scenario: Removed content cannot be shared again
    Given the Hub has the version "gone" of "yt:hrep01xxxxx" with:
      | field   | value   |
      | content | raw-1   |
      | state   | removed |
    And a bundle "again" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | content |
      | transcript | source  | en   | raw-1   |
    When the hub member "alice" shares the bundle "again"
    Then the response status is 409
    And the response contains:
      """
      { "code": "removed_content", "message": "這份內容已依下架申請移除，不能再上傳。" }
      """

  @HUB-02 @HREP-R10
  Scenario: Withdrawing the only share hides the version
    Given the hub member "bob" signed in with "github"
    And a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the hub member "alice" shares the bundle "talk"
    And the Hub version of "yt:hrep01xxxxx" for "transcript:source:en" is known as "raw"
    When the hub member "alice" sends "DELETE /api/v1/shares/{share:last}"
    Then the response status is 204
    And the Hub version "raw" is "withdrawn"
    When the hub member "bob" sends "GET /api/v1/versions/{version:raw}"
    Then the response status is 404

  @HUB-02 @HREP-R10
  Scenario: Withdrawing keeps a version that others also shared
    Given the hub member "bob" signed in with "github"
    And the hub member "bob" is in the group "contributors"
    And a bundle "b" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | content |
      | transcript | source  | en   | raw-1   |
    And a bundle "a" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang | content |
      | transcript | source  | en   | raw-1   |
    And the hub member "bob" shares the bundle "b"
    And the hub member "alice" shares the bundle "a"
    And the Hub version of "yt:hrep01xxxxx" for "transcript:source:en" is known as "raw"
    When the hub member "alice" sends "DELETE /api/v1/shares/{share:last}"
    Then the response status is 204
    And the Hub version "raw" is "visible"

  # --- 限流與對外連線（HREP-R11、R12） ---

  @HUB-02 @HREP-R11
  Scenario: Shares are rate limited per account
    Given the Hub config contains:
      """
      rate_limits: { shares_per_hour: 1 }
      """
    And a bundle "a" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And a bundle "b" of "yt:hrep02xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the hub member "alice" shares the bundle "a"
    When the hub member "alice" shares the bundle "b"
    Then the response status is 429
    And the response contains:
      """
      { "code": "rate_limited", "retry_after_s": "*" }
      """

  @HUB-02 @HREP-R12
  Scenario: Links inside a shared note are never fetched
    Given a bundle "talk" of "yt:hrep01xxxxx" with the artifacts:
      | kind       | variant | lang |
      | transcript | source  | en   |
    And the bundle "talk" has a note containing:
      """
      ## Intro
      ![diagram](http://169.254.169.254/latest/meta-data/)
      See [the feed](https://feeds.example.com/private.xml).
      """
    When the hub member "alice" shares the bundle "talk"
    Then the response status is 201
    And no connection was made to "169.254.169.254"
    And no connection was made to "feeds.example.com"

  # --- 投票、檢舉與自動隱藏（HREP-R13～R15） ---

  @HUB-06 @HREP-R13
  Scenario: Members vote once per version and the score adds up
    Given the hub member "bob" signed in with "github"
    And the hub member "carol" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    When the hub member "bob" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": 1 }
      """
    And the hub member "bob" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": 1 }
      """
    And the hub member "carol" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": -1 }
      """
    And the hub member "carol" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": 1 }
      """
    And the hub member "bob" sends "GET /api/v1/versions/{version:raw}"
    Then the response contains:
      """
      { "score": 2 }
      """

  @HUB-06 @HREP-R13
  Scenario: Uploaders cannot vote on their own version
    Given the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    When the hub member "alice" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": 1 }
      """
    Then the response status is 403
    And the response contains:
      """
      { "code": "own_version", "message": "不能對自己上傳的版本投票。" }
      """

  @HUB-06 @HREP-R13
  Scenario: New accounts cannot vote yet
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    When the hub member "bob" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": 1 }
      """
    Then the response status is 403
    And the response contains:
      """
      { "code": "account_too_new", "message": "帳號建立未滿 24 小時，還不能投票或檢舉。" }
      """

  @HUB-06 @HREP-R13 @HACC-R17
  Scenario: Votes from suspended accounts do not count
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    And the hub member "bob" sends "PUT /api/v1/versions/{version:raw}/vote" with:
      """
      { "value": 1 }
      """
    And the hub member "bob" is suspended
    When the hub member "alice" sends "GET /api/v1/versions/{version:raw}"
    Then the response contains:
      """
      { "score": 0 }
      """

  @HUB-06 @HREP-R14
  Scenario: A member reports a bad version
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    When the hub member "bob" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "wrong_language", "note": "This is Spanish, not English." }
      """
    Then the response status is 201
    When the hub member "bob" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "bad_quality" }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "already_reported", "message": "你已經檢舉過這個版本，管理員處理前不能重複檢舉。" }
      """

  @HUB-06 @HUB-07 @HREP-R14
  Scenario: Copyright problems go through the takedown form
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    When the hub member "bob" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "copyright" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "use_takedown", "message": "版權與隱私問題請改用下架申請。", "actions": ["提出下架申請"] }
      """

  @HUB-06 @HREP-R14
  Scenario: Uploaders do not learn who reported them
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    And the hub member "bob" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "spam", "note": "reported by bob" }
      """
    When the hub member "alice" sends "GET /api/v1/versions/{version:raw}"
    Then the response status is 200
    And the response does not contain "bob"

  @HUB-06 @HUB-07 @HREP-R15
  Scenario: Enough reports from established accounts hide a version until a moderator decides
    Given the Hub config contains:
      """
      moderation: { auto_hide_reports: 2, auto_hide_min_account_age: "168h" }
      """
    And the hub member "bob" signed in with "github"
    And the hub member "carol" signed in with "github"
    And the hub member "dave" signed in with "github"
    And the hub member "mod" signed in with "github"
    And the hub member "mod" is in the group "moderators"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "169h"
    When the hub member "bob" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "spam" }
      """
    And the hub member "carol" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "spam" }
      """
    Then the Hub version "raw" is "hidden"
    When the hub member "dave" sends "GET /api/v1/versions/{version:raw}"
    Then the response status is 404
    When the hub member "mod" sends "POST /api/v1/admin/reports/{report:last}/decision" with:
      """
      { "decision": "dismiss" }
      """
    Then the response status is 200
    And the Hub version "raw" is "visible"

  @HUB-06 @HREP-R15
  Scenario: Reports from new accounts do not hide a version
    Given the Hub config contains:
      """
      moderation: { auto_hide_reports: 2, auto_hide_min_account_age: "168h" }
      """
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    And the clock advances by "25h"
    And the hub member "bob" signed in with "github"
    And the hub member "carol" signed in with "github"
    And the clock advances by "25h"
    When the hub member "bob" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "spam" }
      """
    And the hub member "carol" sends "POST /api/v1/versions/{version:raw}/reports" with:
      """
      { "reason": "spam" }
      """
    Then the Hub version "raw" is "visible"

  # --- 篩選、詳情與取用（HREP-R16～R18） ---

  @HUB-03 @HREP-R16
  Scenario: Versions are filtered by language and model
    Given the Hub has the version "gpt" of "yt:hrep01xxxxx" with:
      | field         | value          |
      | variant       | corrected      |
      | lang          | en             |
      | correct_model | openai/gpt-4.1 |
    And the Hub has the version "claude" of "yt:hrep01xxxxx" with:
      | field         | value                   |
      | variant       | corrected               |
      | lang          | en                      |
      | correct_model | anthropic/claude-sonnet |
    And the Hub has the version "ja" of "yt:hrep01xxxxx" with:
      | field         | value          |
      | variant       | corrected      |
      | lang          | ja             |
      | correct_model | openai/gpt-4.1 |
    When the hub member "alice" sends "GET /api/v1/versions?lang=en&correct_model=openai/gpt-4.1"
    Then the response status is 200
    And the response contains:
      """
      { "versions": [ { "lang": "en", "correct_model": "openai/gpt-4.1" } ] }
      """
    And the response does not contain "anthropic/claude-sonnet"
    And the response does not contain "ja"

  @HUB-03 @HREP-R16
  Scenario: Versions with unverified models can be left out
    Given the Hub has the version "api" of "yt:hrep01xxxxx" with:
      | field          | value          |
      | variant        | corrected      |
      | correct_model  | openai/gpt-4.1 |
      | model_verified | true           |
    And the Hub has the version "agent" of "yt:hrep01xxxxx" with:
      | field          | value           |
      | variant        | corrected       |
      | correct_model  | claude-sonnet-x |
      | model_verified | false           |
    When the hub member "alice" sends "GET /api/v1/versions?platform=youtube&exclude_unverified=true"
    Then the response status is 200
    And the response contains:
      """
      { "versions": [ { "correct_model": "openai/gpt-4.1", "model_verified": true } ] }
      """
    And the response does not contain "claude-sonnet-x"

  @HUB-03 @HREP-R16
  Scenario: Versions are filtered by channel and sorted by score
    Given the Hub has the version "low" of "yt:hrep01xxxxx" with:
      | field   | value                    |
      | channel | UCfixture000000000000000 |
      | score   | 1                        |
    And the Hub has the version "high" of "yt:hrep02xxxxx" with:
      | field   | value                    |
      | channel | UCfixture000000000000000 |
      | score   | 5                        |
    And the Hub has the version "other" of "yt:hrep03xxxxx" with:
      | field   | value                    |
      | channel | UCother00000000000000000 |
      | score   | 9                        |
    When the hub member "alice" sends "GET /api/v1/versions?channel=UCfixture000000000000000"
    Then the response contains:
      """
      { "versions": [ { "score": 5 }, { "score": 1 } ] }
      """
    And the response does not contain "hrep03xxxxx"

  @HUB-03 @HREP-R16
  Scenario: Invalid filters name the parameter at fault
    When the hub member "alice" sends "GET /api/v1/versions?limit=500"
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "query.limit" } ] }
      """

  @HUB-03 @HREP-R17
  Scenario: Version details show provenance and uploaders without their identities
    Given the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field            | value      |
      | uploader         | alice      |
      | transcribe_model | groq/whisper-large-v3 |
    And the hub member "bob" signed in with "github"
    When the hub member "bob" sends "GET /api/v1/versions/{version:raw}"
    Then the response status is 200
    And the response contains:
      """
      { "transcribe_model": "groq/whisper-large-v3", "uploaders": [ { "display_name": "alice" } ], "state": "visible" }
      """
    And the response does not contain "alice@example.com"
    And the response does not contain "github-alice"

  @HUB-03 @HREP-R17
  Scenario: The video page groups every visible version
    Given the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field   | value  |
      | variant | source |
    And the Hub has the version "zh" of "yt:hrep01xxxxx" with:
      | field   | value      |
      | variant | translated |
      | lang    | zh-Hant    |
    When the hub member "alice" sends "GET /api/v1/videos/youtube/hrep01xxxxx"
    Then the response status is 200
    And the response contains:
      """
      { "groups": [ { "kind": "transcript", "variant": "source", "lang": "en" }, { "kind": "transcript", "variant": "translated", "lang": "zh-Hant" } ] }
      """

  @HUB-03 @HREP-R18
  Scenario: Downloads are rendered, attributed and never sniffed
    Given the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field   | value        |
      | license | CC-BY-SA-4.0 |
    And the Hub version "raw" has the cues:
      | # | start        | end          | text            |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello from Hub. |
    When the hub member "alice" sends "GET /api/v1/versions/{version:raw}/download?format=srt"
    Then the response status is 200
    And the response header "Content-Disposition" contains "attachment"
    And the response header "X-Content-Type-Options" contains "nosniff"
    And the response header "X-Notesteme-License" contains "CC-BY-SA-4.0"

  @HUB-03 @HUB-04 @HREP-R18
  Scenario: Raw cues for regeneration need the regenerate permission
    Given the hub member "bob" signed in with "github"
    And the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field    | value |
      | uploader | alice |
    When the hub member "bob" sends "GET /api/v1/versions/{version:raw}/content"
    Then the response status is 403
    And the response contains:
      """
      { "required_permission": "regenerate" }
      """
    When the hub member "alice" sends "GET /api/v1/versions/{version:raw}/content"
    Then the response status is 200

  # --- ASR 前查詢的 Hub 端（HREP-R19） ---

  @HUB-05 @HREP-R19
  Scenario: A lookup answers by hash prefix without naming the video
    Given the Hub has the version "raw" of "yt:hrep01xxxxx" with:
      | field   | value  |
      | variant | source |
    And the Hub has the version "fixed" of "yt:hrep01xxxxx" with:
      | field   | value     |
      | variant | corrected |
    And a Hub token "client" for "alice" with the permissions "regenerate"
    When the Hub client sends "GET /api/v1/lookup?platform=youtube&prefix=9a83" with the Hub token "client"
    Then the response status is 200
    And the response contains:
      """
      { "videos": [ { "video_key_sha256": "*", "versions": [ { "variant": "source", "lang": "en" } ] } ] }
      """
    And the response does not contain "hrep01xxxxx"
    And the response does not contain "corrected"

  @HUB-05 @HREP-R19
  Scenario: A lookup accepts only a four-character prefix
    Given a Hub token "client" for "alice" with the permissions "regenerate"
    When the Hub client sends "GET /api/v1/lookup?platform=youtube&prefix=9a83e44a" with the Hub token "client"
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "query.prefix" } ] }
      """

  # --- Hub UI ---

  @HUB-03 @HUB-06 @ui @manual
  Scenario: Browsing, comparing and voting in the Hub UI
    Given a Hub with several versions of one video from different uploaders and models
    When a member filters by language and correction model and opens the video page
    Then the versions are grouped by kind and language, with scores, licences and the unverified-model badge
    And two versions can be compared side by side on the shared timeline
    And the version page shows the provenance, "regenerated from @alice", download links, vote and report buttons
    And notes are rendered without raw HTML
