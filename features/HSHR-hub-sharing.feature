@M5
Feature: Sharing to the Hub and reusing subtitles from it
  As the owner, I want to share my corrected subtitles and notes to the Hub with their provenance,
  to regenerate a video from someone else's raw subtitles,
  and to have my client check the Hub before paying for ASR.

  Background:
    Given the owner's client is connected to a Hub
    And the owner is signed in
    And a YouTube video "yt:hshr01xxxxx" titled "Shared Talk"

  # --- 連線設定（HSHR-R1） ---

  @HUB-02 @HSHR-R1
  Scenario: The Hub URL must be an https address
    Given the config file contains:
      """
      hub:
        url: http://hub.example.org
      """
    When the server starts
    Then the server fails to start with "設定 hub.url 必須是 https 網址。"

  @HUB-02 @HSHR-R1
  Scenario: The Hub token is stored but never shown again
    When the owner sends "PUT /api/settings/hub" with:
      """
      { "token": "nsh_fixtureTokenValue0000000000000000000000" }
      """
    Then the response status is 200
    When the owner sends "GET /api/settings/hub"
    Then the response status is 200
    And the response contains:
      """
      { "url": "https://hub.example.org", "token_set": true }
      """
    And the response does not contain "nsh_fixtureTokenValue0000000000000000000000"

  @HUB-02 @HSHR-R1
  Scenario: Sharing without a Hub token explains what to set up
    Given the client's Hub token is not set
    And the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text            |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "hub_not_configured", "message": "尚未設定 Hub。請在設定檔加入 hub.url，並在設定頁填入 Hub token。", "actions": ["設定 Hub"] }
      """
    And the Hub received 0 requests

  # --- 分享（HSHR-R2～R8） ---

  @HUB-02 @HSHR-R2 @HSHR-R5
  Scenario: Sharing sends the current subtitles and records the share
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    And the "translated" transcript of "yt:hshr01xxxxx" in "zh-Hant" is:
      | # | start        | end          | text     |
      | 1 | 00:00:01.000 | 00:00:03.000 | 大家好。 |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 201
    And the response contains:
      """
      { "share_id": "*", "versions": [ { "kind": "transcript", "variant": "corrected", "lang": "en", "deduplicated": false } ] }
      """
    And the Hub received a share of "yt:hshr01xxxxx" with the artifacts:
      | kind       | variant    | lang    |
      | transcript | corrected  | en      |
      | transcript | translated | zh-Hant |
    When the owner sends "GET /api/items/yt:hshr01xxxxx"
    Then the response contains:
      """
      { "hub_shares": [ { "share_id": "*", "hub_url": "https://hub.example.org" } ] }
      """

  @HUB-02 @HSHR-R2
  Scenario: Sharing needs an admin token
    Given a token "agent" with scopes "read,ingest"
    And the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the client sends "POST /api/items/yt:hshr01xxxxx/hub/share" with the token "agent" and:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """

  @HUB-02 @HSHR-R2 @cli
  Scenario: Sharing from the CLI
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    And the server is running
    When the owner runs "notesteme hub share yt:hshr01xxxxx --license CC-BY-4.0 --yes --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "share_id": "*" }
      """

  @HUB-02 @HSHR-R3
  Scenario: Sharing needs a confirmation before anything is sent
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "attest_required", "message": "分享前要確認你有權分享這些內容。" }
      """
    And the Hub received 0 requests

  @HUB-02 @HSHR-R3
  Scenario Outline: Sharing needs a supported licence
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { <license> "attest": true }
      """
    Then the response status is 422
    And the response contains:
      """
      <expected>
      """
    And the Hub received 0 requests

    Examples:
      | license                    | expected                                                                  |
      |                            | { "errors": [ { "path": "license", "message": "請選擇授權方式。" } ] }    |
      | "license": "All rights reserved", | { "code": "license_invalid", "message": "不支援的授權：All rights reserved。" } |

  @HUB-02 @HSHR-R4
  Scenario: Members-only videos are not shared
    Given a YouTube video "yt:hshr02xxxxx" with:
      | field        | value           |
      | title        | Members Only    |
      | availability | subscriber_only |
    And the "corrected" transcript of "yt:hshr02xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, members.  |
    When the owner sends "POST /api/items/yt:hshr02xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "not_shareable", "message": "非公開影音不能分享到 Hub。" }
      """
    And the Hub received 0 requests

  @HUB-02 @HSHR-R4
  Scenario: Local files are not shared
    Given a local file "lecture.mp4" in the inbox
    And the "corrected" transcript of "local:lecture.mp4" is:
      | # | start        | end          | text           |
      | 1 | 00:00:01.000 | 00:00:03.000 | Welcome back.  |
    When the owner sends "POST /api/items/local:lecture.mp4/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "not_shareable", "message": "本機檔案不能分享到 Hub。" }
      """
    And the Hub received 0 requests

  @HUB-02 @HSHR-R4 @HSHR-R7
  Scenario: Subtitles that needed a login are not shared
    Given the source "bilibili-ai" requires the cookie "bilibili"
    And a Bilibili video "BV1hs4y1c7mD" with 1 part
    And "bili:BV1hs4y1c7mD/p1" has auto captions in "zh-Hans"
    And the source "bilibili-ai" returns "bilibili-ai-zh.json" for "bili:BV1hs4y1c7mD/p1"
    And the owner sends "POST /api/cookies" with:
      """
      { "platform": "bilibili", "content": "SESSDATA=fixture" }
      """
    And the item "bili:BV1hs4y1c7mD/p1" has been processed up to "normalize"
    Then the "source" artifact of "bili:BV1hs4y1c7mD/p1" has the provenance:
      | field           | value       |
      | source          | bilibili-ai |
      | cookie_required | true        |
    When the owner sends "POST /api/items/bili:BV1hs4y1c7mD/p1/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "not_shareable", "message": "需要登入才能取得的字幕不能分享到 Hub。" }
      """
    And the Hub received 0 requests

  @HUB-02 @HSHR-R4
  Scenario: Items in the trash are not shared
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    And the owner sends "DELETE /api/items/yt:hshr01xxxxx"
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 404
    And the response contains:
      """
      { "code": "in_trash" }
      """

  @HUB-02 @HSHR-R5
  Scenario: Only the chosen artifacts are shared
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    And the "translated" transcript of "yt:hshr01xxxxx" in "zh-Hant" is:
      | # | start        | end          | text     |
      | 1 | 00:00:01.000 | 00:00:03.000 | 大家好。 |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true, "artifacts": [ { "kind": "transcript", "variant": "translated", "lang": "zh-Hant" } ] }
      """
    Then the response status is 201
    And the Hub received a share of "yt:hshr01xxxxx" with the artifacts:
      | kind       | variant    | lang    |
      | transcript | translated | zh-Hant |

  @HUB-02 @HSHR-R5
  Scenario: Asking for an artifact that does not exist is explained
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true, "artifacts": [ { "kind": "transcript", "variant": "corrected", "lang": "ja" } ] }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "artifact_not_found", "message": "這部影音沒有「corrected（ja）」的字幕。" }
      """
    And the Hub received 0 requests

  @HUB-02 @HSHR-R6
  Scenario: The shared bundle carries only allowlisted data
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 201
    And the last bundle sent to the Hub has the manifest fields:
      | field                         | value          |
      | license                       | CC-BY-4.0      |
      | item.platform                 | youtube        |
      | item.external_id              | hshr01xxxxx    |
      | item.availability             | public         |
      | created_by.app_version        | *              |
      | created_by.instance_id        | <absent>       |
      | artifacts.0.provenance.parents_sha256 | *      |
    And the last bundle sent to the Hub does not contain "/data/"
    And the last bundle sent to the Hub does not contain "nsh_"

  @HUB-02 @HSHR-R7
  Scenario: Public captions do not count as needing a login
    Given a YouTube video "yt:hshr04xxxxx" titled "Public Captions"
    And "yt:hshr04xxxxx" has manual captions in "en"
    And the owner sends "POST /api/cookies" with:
      """
      { "platform": "youtube", "content": "SID=fixture" }
      """
    When the owner ingests "yt:hshr04xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the "source" artifact of "yt:hshr04xxxxx" has the provenance:
      | field           | value          |
      | source          | youtube-manual |
      | cookie_required | false          |

  @HUB-02 @HSHR-R8
  Scenario Outline: Hub errors are explained with their class
    Given the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    And the Hub fails with HTTP <status>
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 502
    And the response contains:
      """
      { "code": "<code>", "message": "<message>" }
      """

    Examples:
      | status | code             | message                                                          |
      | 503    | hub_unavailable  | 無法連線到 Hub（HTTP 503）。請稍後再試。                         |
      | 401    | hub_unauthorized | Hub token 無效或已過期。請在 Hub 建立新的 token，再到設定頁更新。 |
      | 429    | hub_rate_limited | Hub 暫時拒絕請求（太頻繁），請在 30 秒後再試。                   |

  @HUB-02 @HSHR-R8
  Scenario: A Hub token without the upload permission is explained
    Given the client's Hub token has the permissions "read,download"
    And the "corrected" transcript of "yt:hshr01xxxxx" is:
      | # | start        | end          | text             |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello, everyone. |
    When the owner sends "POST /api/items/yt:hshr01xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 502
    And the response contains:
      """
      { "code": "hub_forbidden", "message": "Hub token 沒有「upload」權限。請在 Hub 建立有這個權限的 token。" }
      """

  # --- 重新生成（HSHR-R9～R11） ---

  @HUB-04 @HSHR-R9
  Scenario: Regenerating from someone else's raw subtitles skips transcription
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And "yt:hshr03xxxxx" has no caption tracks
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field            | value                 |
      | variant          | source                |
      | uploader         | alice                 |
      | transcribe_model | groq/whisper-large-v3 |
    And the Hub version "alice-raw" has the cues:
      | # | start        | end          | text              |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello from Alice. |
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-raw}" }
      """
    Then the response status is 202
    When the worker runs until idle
    Then the transcript of "yt:hshr03xxxxx" came from "hub"
    And the source "supadata" was called 0 times
    And the source "groq" was called 0 times
    And the "source" transcript of "yt:hshr03xxxxx" is:
      | # | start        | end          | text              |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello from Alice. |
    And the "source" artifact of "yt:hshr03xxxxx" has the provenance:
      | field               | value                   |
      | executor            | hub                     |
      | source              | hub                     |
      | hub_origin.uploader | alice                   |
      | hub_origin.url      | https://hub.example.org |
    And the stage "normalize" of "yt:hshr03xxxxx" is "done"

  @HUB-04 @HSHR-R9
  Scenario: Only raw subtitles can be used to regenerate
    Given the Hub has the version "alice-fixed" of "yt:hshr03xxxxx" with:
      | field   | value     |
      | variant | corrected |
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-fixed}" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "not_raw", "message": "只能用別人的原始字幕（source 或 normalized）重新生成。" }
      """
    And the library contains 0 items

  @HUB-04 @HSHR-R9
  Scenario: Regenerating needs a token that may start paid work
    Given a token "reader" with scopes "read"
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    When the client sends "POST /api/hub/import" with the token "reader" and:
      """
      { "version": "{version:alice-raw}" }
      """
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "ingest" }
      """

  @HUB-04 @HSHR-R9 @cli
  Scenario: Regenerating from the CLI
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    And the server is running
    When the owner runs "notesteme hub import {version:alice-raw} --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "item": { "ref": "yt:hshr03xxxxx" } }
      """

  @HUB-04 @HSHR-R10
  Scenario: Content that does not match its hash is refused
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    And the Hub serves tampered content for the version "alice-raw"
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-raw}" }
      """
    Then the response status is 502
    And the response contains:
      """
      { "code": "hub_integrity", "message": "Hub 回傳的內容與版本的 sha256 不符，已拒絕使用。" }
      """
    And the library contains 0 items

  @HUB-04 @HSHR-R10
  Scenario: Malformed cues from the Hub are refused
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    And the Hub version "alice-raw" has the cues:
      | # | start        | end          | text            |
      | 1 | 00:00:05.000 | 00:00:02.000 | Backwards time. |
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-raw}" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "hub_content_invalid" }
      """
    And the library contains 0 items

  @HUB-04 @HSHR-R8 @HSHR-R10
  Scenario: A version that is gone from the Hub is explained
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "0193a3b4-0000-7000-8000-00000000dead" }
      """
    Then the response status is 404
    And the response contains:
      """
      { "code": "hub_version_not_found", "message": "Hub 上找不到這個版本，可能已被移除或你沒有權限查看。" }
      """

  @HUB-04 @HSHR-R11
  Scenario: Regenerating an existing item keeps its older transcript
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And "yt:hshr03xxxxx" has manual captions in "en"
    And the item "yt:hshr03xxxxx" has been processed up to "normalize"
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-raw}" }
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr03xxxxx" came from "hub"
    And the "source" artifact of "yt:hshr03xxxxx" has 2 versions

  @HUB-04 @HSHR-R11
  Scenario: Regenerating an item in the trash points to the trash
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And "yt:hshr03xxxxx" has manual captions in "en"
    And the item "yt:hshr03xxxxx" has been processed up to "transcript"
    And the owner sends "DELETE /api/items/yt:hshr03xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    When the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-raw}" }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "in_trash", "actions": ["還原"] }
      """

  @HUB-02 @HUB-04 @HSHR-R5 @HSHR-R6
  Scenario: Re-sharing a regenerated item leaves out the raw subtitles and credits their origin
    Given a YouTube video "yt:hshr03xxxxx" titled "Alice's Talk"
    And "yt:hshr03xxxxx" has no caption tracks
    And the Hub has the version "alice-raw" of "yt:hshr03xxxxx" with:
      | field   | value  |
      | variant | source |
    And the owner sends "POST /api/hub/import" with:
      """
      { "version": "{version:alice-raw}" }
      """
    And the worker runs until idle
    And the "corrected" transcript of "yt:hshr03xxxxx" is:
      | # | start        | end          | text              |
      | 1 | 00:00:01.000 | 00:00:03.000 | Hello from Alice! |
    When the owner sends "POST /api/items/yt:hshr03xxxxx/hub/share" with:
      """
      { "license": "CC-BY-4.0", "attest": true }
      """
    Then the response status is 201
    And the Hub received a share of "yt:hshr03xxxxx" with the artifacts:
      | kind       | variant    | lang |
      | transcript | normalized | en   |
      | transcript | corrected  | en   |
    And the last bundle sent to the Hub has the manifest fields:
      | field                                      | value |
      | artifacts.0.provenance.hub_origin.version_id | *   |

  # --- ASR 前查詢（HSHR-R12～R15） ---

  @HUB-05 @HSHR-R12
  Scenario: Turning on the Hub lookup requires accepting what it sends
    When the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "acknowledgement_required", "disclosure_version": "hub-precheck-v2", "disclosure": "*" }
      """
    When the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    Then the response status is 200
    When the owner sends "GET /api/settings/hub"
    Then the response contains:
      """
      { "token_set": true, "precheck": { "enabled": true, "disclosure_version": "hub-precheck-v2" } }
      """

  @HUB-05 @HSHR-R12
  Scenario: The Hub lookup is off by default
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:hshr05xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "groq"
    And the Hub was asked for a lookup 0 times

  @HUB-05 @HSHR-R13 @HSHR-R14
  Scenario: Before paying for ASR the client reuses a raw transcript from the Hub
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "hub"
    And the source "supadata" was called 0 times
    And the source "groq" was called 0 times
    And the Hub was asked for a lookup 1 time
    And the Hub never received the video ID of "yt:hshr05xxxxx"

  @HUB-05 @HSHR-R13
  Scenario: Platform captions are used without asking the Hub
    Given "yt:hshr01xxxxx" has manual captions in "en"
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    When the owner ingests "yt:hshr01xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr01xxxxx" came from "youtube-manual"
    And the Hub was asked for a lookup 0 times

  @HUB-05 @HSHR-R13
  Scenario: Local files are never looked up
    Given a local file "meetup.mp4" in the inbox
    And the source "groq" returns "groq-en.json" for "local:meetup.mp4"
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    When the owner ingests "local:meetup.mp4" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "local:meetup.mp4" came from "groq"
    And the Hub was asked for a lookup 0 times

  @HUB-05 @HSHR-R14
  Scenario: The client prefers the raw source in the original language
    Given a YouTube video "yt:hshr06xxxxx" titled "Two Versions"
    And "yt:hshr06xxxxx" has no caption tracks
    And the Hub has the version "norm" of "yt:hshr06xxxxx" with:
      | field   | value      |
      | variant | normalized |
      | score   | 9          |
    And the Hub version "norm" has the cues:
      | # | start        | end          | text        |
      | 1 | 00:00:01.000 | 00:00:03.000 | Normalized. |
    And the Hub has the version "raw" of "yt:hshr06xxxxx" with:
      | field   | value  |
      | variant | source |
      | score   | 1      |
    And the Hub version "raw" has the cues:
      | # | start        | end          | text     |
      | 1 | 00:00:01.000 | 00:00:03.000 | Raw one. |
    And the Hub has the version "ja" of "yt:hshr06xxxxx" with:
      | field   | value  |
      | variant | source |
      | lang    | ja     |
      | score   | 20     |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    When the owner ingests "yt:hshr06xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr06xxxxx" came from "hub"
    And the "source" transcript of "yt:hshr06xxxxx" is:
      | # | text     |
      | 1 | Raw one. |

  @HUB-05 @HSHR-R14
  Scenario: Without a matching language the client pays as usual
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:hshr05xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "ja" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
      | lang    | ja     |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "groq"
    And the Hub was asked for a lookup 1 time

  @HUB-05 @HSHR-R15
  Scenario: A Hub that fails falls back to the paid source with a note
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:hshr05xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    And the Hub fails with HTTP 503
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "groq"
    And the stage "transcript" of "yt:hshr05xxxxx" is "done"
    And "yt:hshr05xxxxx" shows the message "無法查詢 Hub（HTTP 503），這次改用付費來源。"

  @HUB-05 @HSHR-R15
  Scenario: A Hub that does not answer in time falls back to the paid source
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:hshr05xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    And the Hub does not respond
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "groq"
    And the stage "transcript" of "yt:hshr05xxxxx" is "done"
    And "yt:hshr05xxxxx" shows the message "無法查詢 Hub（連線逾時），這次改用付費來源。"

  @HUB-05 @HSHR-R15
  Scenario: A Hub token without the regenerate permission falls back to the paid source
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:hshr05xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    And the client's Hub token has the permissions "read"
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "groq"
    And the stage "transcript" of "yt:hshr05xxxxx" is "done"
    And "yt:hshr05xxxxx" shows the message "無法查詢 Hub（token 無效或權限不足），這次改用付費來源。"

  @HUB-05 @HSHR-R15
  Scenario: Tampered Hub content falls back to the paid source
    Given a YouTube video "yt:hshr05xxxxx" titled "No Captions"
    And "yt:hshr05xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:hshr05xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:hshr05xxxxx"
    And the Hub has the version "alice-raw" of "yt:hshr05xxxxx" with:
      | field   | value  |
      | variant | source |
    And the owner sends "PUT /api/settings/hub" with:
      """
      { "precheck": { "enabled": true, "acknowledge": "hub-precheck-v2" } }
      """
    And the Hub serves tampered content for the version "alice-raw"
    When the owner ingests "yt:hshr05xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:hshr05xxxxx" came from "groq"
    And the stage "transcript" of "yt:hshr05xxxxx" is "done"
    And "yt:hshr05xxxxx" shows the message "無法查詢 Hub（內容驗證失敗），這次改用付費來源。"

  # --- UI 與端到端 ---

  @HUB-02 @HUB-04 @HUB-05 @ui @manual
  Scenario: Sharing, regenerating and the Hub settings in the client UI
    Given the client is connected to a Hub
    When the owner opens "Share to Hub" on an item
    Then the dialog lists the artifacts, the licence, the confirmation checkbox and a preview of the fields that will be sent
    And an item transcribed from the Hub shows "From Hub: @alice's raw subtitles"
    And the Hub section of the settings shows the URL read-only, the token, a connection test, and the lookup switch with its disclosure

  @HUB-02 @HUB-04 @manual
  Scenario: The M5 acceptance check with a real Hub
    Given a real Hub with two members and two client instances
    When the first owner shares the corrected subtitles and note of a public video
    And the second owner regenerates the same video from the first owner's raw subtitles
    Then the second client produces its own corrected subtitles without paying for transcription
    And the Hub shows the second version as regenerated from the first owner's subtitles
