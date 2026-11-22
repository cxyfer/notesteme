@M4
Feature: Summaries and digests
  As the owner, I want new subscription items summarised and sent to me as a
  daily or weekly digest, and a profile that pauses after the summary, so that
  only the items I pick go on to cost more.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:dig01xxxxxx" with:
      | field    | value                  |
      | title    | Building a Tiny Kernel |
      | duration | 600                    |
    And "yt:dig01xxxxxx" has auto captions in "en"

  @DIG-01 @DIG-R1
  Scenario: The summary stage is off unless the profile turns it on
    Given the summary profile is:
      """
      stages:
        summary: { language: zh-Hant }
      """
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    Then the stage "summary" of "yt:dig01xxxxxx" is skipped with the reason "off"
    And the LLM provider "anthropic" received 0 requests for "summary"

  @DIG-02 @DIG-R1
  Scenario: Pausing after the summary needs the summary to be on
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default }
      """
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "bad-pause", "config": { "stages": { "summary": { "executor": "off", "pause_after": true } } } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "stages.summary.pause_after", "message": "摘要關閉時不能設定摘要後暫停。" } ] }
      """

  @DIG-01 @DIG-R2 @DIG-R3 @DIG-R5
  Scenario: The summary is made in one request from the normalized cues
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, language: zh-Hant }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    Then the stage "summary" of "yt:dig01xxxxxx" is "done"
    And the LLM provider "anthropic" received 1 request for "summary"
    And request 1 to "anthropic" contains "Building a Tiny Kernel"
    And request 1 to "anthropic" contains "zh-Hant"
    When the owner sends "GET /api/items/yt:dig01xxxxxx"
    Then the response contains:
      """
      { "summary": {
          "text": "這部影片從零開始寫一個小型核心：先設定交叉編譯工具鏈，再寫開機載入器，最後跳進核心的進入點。",
          "key_points": ["設定交叉編譯工具鏈", "撰寫開機載入器", "跳進核心的進入點"],
          "title_translation": "打造一個迷你核心",
          "language": "zh-Hant"
      } }
      """

  @DIG-01 @DIG-R2
  Scenario: Videos longer than the limit are not summarised
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default }
      """
    And a YouTube video "yt:dig09xxxxxx" with:
      | field    | value             |
      | title    | A Very Long Lecture |
      | duration | 9000                |
    And "yt:dig09xxxxxx" has auto captions in "en"
    When the owner ingests "yt:dig09xxxxxx"
    And the worker runs until idle
    Then the stage "summary" of "yt:dig09xxxxxx" is skipped with the reason "too_long"
    And "yt:dig09xxxxxx" shows the message "影片長度 2:30:00 超過摘要的上限 2:00:00，略過摘要。"
    And the LLM provider "anthropic" received 0 requests for "summary"

  @DIG-01 @DIG-R3 @DIG-R4
  Scenario: A summary that keeps failing validation leaves the item without a summary
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-too-many-points"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    Then the stage "summary" of "yt:dig01xxxxxx" is "done"
    And the LLM provider "anthropic" received 3 requests for "summary"
    And request 2 to "anthropic" contains "key_points 有 8 項，上限是 7 項"
    And the item "yt:dig01xxxxxx" has no "summary" artifact
    And "yt:dig01xxxxxx" shows the message "無法產生摘要：模型回傳的內容沒有通過驗證（key_points 有 8 項，上限是 7 項）。"
    And "yt:dig01xxxxxx" offers the actions "重新產生摘要, 開啟 Prompt"

  @DIG-01 @DIG-R3
  Scenario: No title translation is kept when the summary is in the original language
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, language: en }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:dig01xxxxxx"
    Then the response contains:
      """
      { "summary": { "title_translation": null, "language": "en" } }
      """

  @DIG-01 @DIG-R5
  Scenario: The summary is recorded in the provenance
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, language: zh-Hant }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    Then the "summary" artifact of "yt:dig01xxxxxx" has the provenance:
      | field            | value          |
      | stage            | summary        |
      | provider         | anthropic      |
      | prompt.id        | summary-default |
      | parents          | normalized     |
      | summary.language | zh-Hant        |

  @DIG-02 @DIG-R6
  Scenario: A profile that pauses after the summary stops before segment
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    Then the stage "summary" of "yt:dig01xxxxxx" is "done"
    And the stage "segment" of "yt:dig01xxxxxx" is blocked with the reason "awaiting_user"
    And "yt:dig01xxxxxx" shows the message "摘要完成，等你決定要不要繼續處理。"
    And "yt:dig01xxxxxx" offers the actions "繼續處理, 取消"
    And the LLM provider "openai" received 0 requests

  @DIG-02 @DIG-R6 @DIG-R7
  Scenario: Items that are not picked never cost more
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    And the clock is at "2026-11-23T10:00:00Z"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    And the server restarts
    And the clock advances by "72h"
    And the worker runs until idle
    Then the stage "segment" of "yt:dig01xxxxxx" is blocked with the reason "awaiting_user"
    And the LLM provider "openai" received 0 requests

  @DIG-02 @DIG-R6
  Scenario: Rerunning from segment does not pause again
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    And the LLM provider "openai" answers "segment" with "segment-keep-all"
    And the item "yt:dig01xxxxxx" has been processed up to "summary"
    When the owner reruns "yt:dig01xxxxxx" from "segment"
    And the worker runs until idle
    Then the stage "segment" of "yt:dig01xxxxxx" is "done"

  @DIG-02 @DIG-R7
  Scenario: Continuing a paused item runs the rest of the pipeline
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    And the LLM provider "openai" answers "segment" with "segment-keep-all"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    And the owner sends "POST /api/items/yt:dig01xxxxxx/continue"
    Then the response status is 202
    And the response contains:
      """
      { "continued": "segment" }
      """
    When the worker runs until idle
    Then the stage "segment" of "yt:dig01xxxxxx" is "done"
    And the LLM provider "anthropic" received 1 request for "summary"

  @DIG-02 @DIG-R7
  Scenario: Only items that are waiting can be continued
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    And the owner sends "POST /api/items/yt:dig01xxxxxx/continue"
    Then the response status is 409
    And the response contains:
      """
      { "code": "not_awaiting_user", "message": "這部影音沒有在等待你決定是否繼續。" }
      """

  @DIG-02 @DIG-R7
  Scenario: Several items are continued at once and the others are reported
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    And a YouTube video "yt:dig02xxxxxx" titled "Not paused"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    And the owner sends "POST /api/items/continue" with:
      """
      { "ids": ["yt:dig01xxxxxx", "yt:dig02xxxxxx"] }
      """
    Then the response status is 200
    And the response contains:
      """
      { "continued": ["yt:dig01xxxxxx"], "skipped": [ { "id": "yt:dig02xxxxxx", "code": "not_found" } ] }
      """

  @mcp @DIG-02 @DIG-R7
  Scenario: Continuing through MCP needs the ingest scope
    Given the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
      """
    And a token "reader" with scopes "read"
    When the MCP client with the token "reader" calls "continue_item" with:
      """
      { "item": "yt:dig01xxxxxx" }
      """
    Then the tool call fails with "ingest"

  @DIG-01 @DIG-R8 @DIG-R10
  Scenario: A daily digest is sent at the local time of its time zone
    Given the notification endpoints are reachable
    And the config file contains:
      """
      server:
        public_url: https://notes.example.com
      """
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "Asia/Taipei", "notifiers": ["phone"], "scope": "all" }
      """
    And the clock is at "2026-11-23T23:30:00Z"
    And the summary of "yt:dig01xxxxxx" is:
      """
      { "summary": "從零開始寫一個小型核心。", "key_points": [], "title_translation": "打造一個迷你核心" }
      """
    When the clock advances by "29m"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 0 requests
    When the clock advances by "1m"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 1 request
    And the last request to the notification endpoint "https://ntfy.sh/" contains:
      """
      { "topic": "notesteme-owner", "title": "Notesteme 每日彙整：2026-11-24（1 部）" }
      """
    And the notification endpoint "https://ntfy.sh/" received a request containing "Building a Tiny Kernel（打造一個迷你核心）"
    And the notification endpoint "https://ntfy.sh/" received a request containing "從零開始寫一個小型核心。"
    And the notification endpoint "https://ntfy.sh/" received a request containing "https://notes.example.com/items/"

  @DIG-01 @DIG-R8
  Scenario: A weekly digest waits for its weekday
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "weekly" is:
      """
      { "period": "weekly", "weekday": "mon", "at": "09:00", "timezone": "UTC", "notifiers": ["phone"], "scope": "all" }
      """
    And the clock is at "2026-11-24T10:00:00Z"
    And the summary of "yt:dig01xxxxxx" is:
      """
      { "summary": "從零開始寫一個小型核心。", "key_points": [], "title_translation": null }
      """
    When the clock advances by "142h"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 0 requests
    When the clock advances by "1h"
    And the worker runs until idle
    Then the last request to the notification endpoint "https://ntfy.sh/" contains:
      """
      { "title": "Notesteme 每週彙整：2026-11-23～2026-11-29（1 部）" }
      """

  @DIG-01 @DIG-R8
  Scenario Outline: Invalid digest settings are rejected with the field at fault
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the notifier "pager" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-pager" }, "events": ["item.failed"] }
      """
    When the owner sends "POST /api/digests" with:
      """
      { "id": "bad", "period": "daily", "at": "08:00", "timezone": "<timezone>", "notifiers": ["<notifier>"] }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "<path>", "message": "<message>" } ] }
      """

    Examples:
      | timezone    | notifier | path      | message                              |
      | Mars/Base   | phone    | timezone  | 不認得的時區：Mars/Base。               |
      | Asia/Taipei | pager    | notifiers | notifier pager 沒有勾選「訂閱彙整」。     |

  @DIG-01 @DIG-R9
  Scenario: Nothing is sent when there is nothing new
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "UTC", "notifiers": ["phone"], "scope": "all" }
      """
    And the clock is at "2026-11-23T09:00:00Z"
    When the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 0 requests
    When the owner sends "GET /api/notifications"
    Then the response contains:
      """
      { "notifications": [] }
      """

  @DIG-01 @DIG-R9
  Scenario: An item appears in only one digest
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "UTC", "notifiers": ["phone"], "scope": "all" }
      """
    And the clock is at "2026-11-23T09:00:00Z"
    And the summary of "yt:dig01xxxxxx" is:
      """
      { "summary": "第一部的摘要。", "key_points": [], "title_translation": null }
      """
    And the clock advances by "24h"
    And the worker runs until idle
    And a YouTube video "yt:dig03xxxxxx" titled "The Second Video"
    And the summary of "yt:dig03xxxxxx" is:
      """
      { "summary": "第二部的摘要。", "key_points": [], "title_translation": null }
      """
    When the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 2 requests
    And the last request to the notification endpoint "https://ntfy.sh/" contains:
      """
      { "title": "Notesteme 每日彙整：2026-11-25（1 部）" }
      """
    And the notification endpoint "https://ntfy.sh/" received a request containing "第二部的摘要。"

  @DIG-01 @DIG-R8 @DIG-R9
  Scenario: The default scope leaves out items that were added by hand
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "UTC", "notifiers": ["phone"] }
      """
    And the clock is at "2026-11-23T09:00:00Z"
    And the summary of "yt:dig01xxxxxx" is:
      """
      { "summary": "手動新增的影音。", "key_points": [], "title_translation": null }
      """
    When the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 0 requests

  @DIG-01 @DIG-R10
  Scenario: Without a public URL the digest has no links
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "UTC", "notifiers": ["phone"], "scope": "all" }
      """
    And the clock is at "2026-11-23T09:00:00Z"
    And the summary of "yt:dig01xxxxxx" is:
      """
      { "summary": "從零開始寫一個小型核心。", "key_points": [], "title_translation": null }
      """
    When the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received a request containing "設定 server.public_url 後，彙整會附上連結。"
    And no request to a notification endpoint contains "/items/"

  @DIG-01 @DIG-02 @DIG-R10
  Scenario: A webhook receives the digest as structured data with login-only links
    Given the notification endpoints are reachable
    And the config file contains:
      """
      server:
        public_url: https://notes.example.com
      """
    And the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-digest-0123456789abcdef" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "UTC", "notifiers": ["automation"], "scope": "all" }
      """
    And the summary profile is:
      """
      stages:
        summary: { executor: api, provider: anthropic, model: fixture-model, prompt: summary-default, pause_after: true }
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      """
    And the LLM provider "anthropic" answers "summary" with "summary-kernel"
    And the clock is at "2026-11-23T09:00:00Z"
    When the owner ingests "yt:dig01xxxxxx"
    And the worker runs until idle
    And the clock advances by "24h"
    And the worker runs until idle
    Then the last request to the notification endpoint "https://hooks.example.com/notesteme" contains:
      """
      { "event": "digest",
        "digest": { "id": "daily", "period": "daily" },
        "items": [ { "title": "Building a Tiny Kernel", "title_translation": "打造一個迷你核心", "awaiting_user": true } ] }
      """
    And the notification endpoint "https://hooks.example.com/notesteme" received a request containing "https://notes.example.com/library?blocked=awaiting_user"
    And no request to a notification endpoint contains "token="
    And no request to a notification endpoint contains "session"

  @DIG-01 @DIG-R11
  Scenario: Previewing a digest does not send it
    Given the notification endpoints are reachable
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["digest"] }
      """
    And the digest "daily" is:
      """
      { "period": "daily", "at": "08:00", "timezone": "UTC", "notifiers": ["phone"], "scope": "all" }
      """
    And the clock is at "2026-11-23T09:00:00Z"
    And the summary of "yt:dig01xxxxxx" is:
      """
      { "summary": "從零開始寫一個小型核心。", "key_points": [], "title_translation": null }
      """
    When the owner sends "POST /api/digests/daily/preview"
    Then the response status is 200
    And the response contains:
      """
      { "items": [ { "title": "Building a Tiny Kernel", "summary": "從零開始寫一個小型核心。" } ] }
      """
    And the notification endpoint "https://ntfy.sh/" received 0 requests
    When the clock advances by "23h"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 1 request

  @ui @manual @DIG-02 @DIG-R7
  Scenario: Paused items are picked from the library
    Given three items are paused after their summary
    When the owner filters the library by "等你決定"
    And selects two items and chooses "繼續處理"
    Then the dialog shows the number of items and the estimated cost
    And after confirming, only the two selected items continue
