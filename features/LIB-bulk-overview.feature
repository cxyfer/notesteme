@M4
Feature: Bulk actions and the overview
  As the owner, I want to act on many items at once and to see what needs my attention first,
  so that I can rerun or delete in bulk and start my day from one page.

  Background:
    Given the owner is signed in
    And the clock is at "2026-11-23T10:00:00Z"
    And the cost estimate of every "correct" task is "0.04" USD
    And the price of "anthropic" for "fixture-model" is "2.00" USD per "input_mtok"
    And the price of "anthropic" for "fixture-model" is "10.00" USD per "output_mtok"
    And the profile "bulk-correct" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default }
      """
    And the default profile is "bulk-correct"
    And a YouTube video "yt:bulk01xxxxx" titled "First talk"
    And a YouTube video "yt:bulk02xxxxx" titled "Second talk"

  @LIB-08 @LIB-R13 @LIB-R14
  Scenario: A dry run shows how many items a rerun touches and what it may cost
    Given the "segmented" transcript of "yt:bulk01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the "segmented" transcript of "yt:bulk02xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "rerun", "ids": ["yt:bulk01xxxxx", "yt:bulk02xxxxx", "yt:bulk01xxxxx"], "from": "correct", "dry_run": true }
      """
    Then the response status is 200
    And the response contains:
      """
      { "action": "rerun", "dry_run": true, "count": 2, "estimated_usd": 0.08, "unknown_estimates": 0,
        "budget": { "remaining_usd": null, "exceeds": false },
        "items": [ { "ref": "yt:bulk01xxxxx", "ok": true }, { "ref": "yt:bulk02xxxxx", "ok": true } ] }
      """
    And the LLM provider "anthropic" received 0 requests

  @LIB-08 @LIB-R14 @LIB-R17
  Scenario: A dry run warns when the rerun would go over the budget
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.05  |
    And the "segmented" transcript of "yt:bulk01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the "segmented" transcript of "yt:bulk02xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "rerun", "ids": ["yt:bulk01xxxxx", "yt:bulk02xxxxx"], "from": "correct", "dry_run": true }
      """
    Then the response contains:
      """
      { "count": 2, "estimated_usd": 0.08, "budget": { "remaining_usd": 0.05, "exceeds": true } }
      """

  @LIB-08 @LIB-R15 @LIB-R17
  Scenario: A bulk rerun reruns every selected item
    Given the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:bulk01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the "segmented" transcript of "yt:bulk02xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "rerun", "ids": ["yt:bulk01xxxxx", "yt:bulk02xxxxx"], "from": "correct" }
      """
    Then the response status is 200
    And the response contains:
      """
      { "action": "rerun", "dry_run": false, "count": 2, "succeeded": 2, "failed": 0 }
      """
    When the worker runs until idle
    Then the stage "correct" of "yt:bulk01xxxxx" is "done"
    And the stage "correct" of "yt:bulk02xxxxx" is "done"

  @LIB-08 @LIB-R17
  Scenario: Changing the profile in bulk reruns with the new profile
    Given the profile "bulk-openai" is:
      """
      stages:
        correct: { executor: api, provider: openai, model: fixture-model, prompt: correct-default }
      """
    And the LLM provider "openai" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:bulk01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "change_profile", "ids": ["yt:bulk01xxxxx"], "profile": "bulk-openai", "from": "correct" }
      """
    And the worker runs until idle
    Then the effective config of "yt:bulk01xxxxx" for "correct" contains:
      """
      provider: openai
      """
    And the LLM provider "anthropic" received 0 requests

  @LIB-08 @LIB-R15 @LIB-R19
  Scenario: One missing item does not stop the others
    Given the item "yt:bulk01xxxxx" has been processed up to "metadata"
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "mark_read", "ids": ["yt:bulk01xxxxx", "yt:missing0000"] }
      """
    Then the response status is 200
    And the response contains:
      """
      { "count": 2, "succeeded": 1, "failed": 1,
        "items": [ { "ref": "yt:bulk01xxxxx", "ok": true },
                   { "ref": "yt:missing0000", "ok": false, "code": "not_found", "message": "找不到這部影音。" } ] }
      """

  @LIB-08 @LIB-R19
  Scenario: Items marked as read can be filtered
    Given the item "yt:bulk01xxxxx" has been processed up to "metadata"
    And the item "yt:bulk02xxxxx" has been processed up to "metadata"
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "mark_read", "ids": ["yt:bulk01xxxxx"] }
      """
    And the owner sends "GET /api/items?read_state=read"
    Then the response status is 200
    And the response contains:
      """
      { "total": 1, "items": [ { "title": "First talk", "read_state": "read" } ] }
      """
    When the owner sends "GET /api/items?read_state=unread"
    Then the response contains:
      """
      { "total": 1, "items": [ { "title": "Second talk", "read_state": "unread" } ] }
      """

  @LIB-08 @LIB-R13
  Scenario Outline: An invalid bulk request runs nothing
    Given the item "yt:bulk01xxxxx" has been processed up to "metadata"
    When the owner sends "POST /api/items/batch" with:
      """
      <body>
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "errors": [ { "path": "<path>", "message": "<message>" } ] }
      """

    Examples:
      | body                                                                                | path    | message                    |
      | { "action": "mark_read", "ids": [] }                                                | ids     | 至少要選一部影音。         |
      | { "action": "pause", "ids": ["yt:bulk01xxxxx"] }                                    | action  | 不支援的批次動作 pause。   |
      | { "action": "rerun", "ids": ["yt:bulk01xxxxx"], "from": "proofread" }               | from    | from 必須是 pipeline 的步驟。 |
      | { "action": "change_profile", "ids": ["yt:bulk01xxxxx"], "profile": "nope", "from": "correct" } | profile | 找不到處理方案 nope。      |

  @LIB-08 @LIB-R16
  Scenario: Each bulk action needs its own scope
    Given the item "yt:bulk01xxxxx" has been processed up to "metadata"
    And a token "worker" with scopes "ingest"
    And a token "viewer" with scopes "read"
    When the client sends "POST /api/items/batch" with the token "worker" and:
      """
      { "action": "delete", "ids": ["yt:bulk01xxxxx"], "dry_run": true }
      """
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """
    When the client sends "POST /api/items/batch" with the token "viewer" and:
      """
      { "action": "mark_read", "ids": ["yt:bulk01xxxxx"] }
      """
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "ingest" }
      """
    And the library contains "yt:bulk01xxxxx"

  @LIB-08 @LIB-R14 @LIB-R18
  Scenario: A bulk delete shows the combined impact and then moves everything to the trash
    Given "yt:bulk01xxxxx" has manual captions in "en"
    And "yt:bulk02xxxxx" has manual captions in "en"
    And the item "yt:bulk01xxxxx" has been processed up to "transcript"
    And the item "yt:bulk02xxxxx" has been processed up to "transcript"
    And the cost ledger has an entry of "0.42" USD for "yt:bulk01xxxxx"
    And the cost ledger has an entry of "0.10" USD for "yt:bulk02xxxxx"
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "delete", "ids": ["yt:bulk01xxxxx", "yt:bulk02xxxxx"], "dry_run": true }
      """
    Then the response contains:
      """
      { "count": 2, "deletion": { "artifacts": "*", "bytes": "*", "spent_usd": 0.52, "outputs": 0, "modified_outputs": 0 } }
      """
    And the trash contains 0 items
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "delete", "ids": ["yt:bulk01xxxxx", "yt:bulk02xxxxx"] }
      """
    Then the response contains:
      """
      { "succeeded": 2, "failed": 0 }
      """
    And the trash contains 2 items
    And the library contains 0 items

  @LIB-08 @LIB-R20
  Scenario: A dry run of a bundle export only counts the items
    Given the item "yt:bulk01xxxxx" has been processed up to "metadata"
    And the item "yt:bulk02xxxxx" has been processed up to "metadata"
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "export_bundle", "ids": ["yt:bulk01xxxxx", "yt:bulk02xxxxx"], "dry_run": true }
      """
    Then the response status is 200
    And the response contains:
      """
      { "action": "export_bundle", "dry_run": true, "count": 2, "estimated_usd": 0 }
      """

  @LIB-09 @LIB-R21
  Scenario: The overview lists what needs the owner first
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.01  |
    And the "segmented" transcript of "yt:bulk01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the owner reruns "yt:bulk01xxxxx" from "correct"
    And the worker runs until idle
    When the owner sends "GET /api/overview"
    Then the response status is 200
    And the response contains:
      """
      { "needs_you": { "total": 1,
                       "items": [ { "ref": "yt:bulk01xxxxx", "title": "First talk", "stage": "correct", "status": "blocked",
                                    "reason": "budget_blocked", "actions": ["調整預算", "查看成本"] } ] },
        "month": { "month": "2026-11", "spent_usd": 0, "monthly_usd": 0.01, "timezone": "UTC" } }
      """

  @LIB-09 @LIB-R21
  Scenario: The overview shows at most three items in progress
    Given a YouTube video "yt:bulk03xxxxx" titled "Third talk"
    And a YouTube video "yt:bulk04xxxxx" titled "Fourth talk"
    And the concurrency limit of "download" is 4
    And the item "yt:bulk01xxxxx" has been ingested
    And the item "yt:bulk02xxxxx" has been ingested
    And the item "yt:bulk03xxxxx" has been ingested
    And the item "yt:bulk04xxxxx" has been ingested
    When the worker starts one round
    And the owner sends "GET /api/overview"
    Then the response contains:
      """
      { "now": { "total": 4, "more": 1, "items": [ { "ref": "*", "stage": "*" }, { "ref": "*" }, { "ref": "*" } ] } }
      """

  @LIB-09 @LIB-R21
  Scenario: Today's events follow the requested time zone
    Given the item "yt:bulk01xxxxx" has been ingested
    When the owner sends "GET /api/overview?tz=Asia/Taipei"
    Then the response status is 200
    And the response contains:
      """
      { "today": [ { "type": "item.created", "at": "*" } ] }
      """
    When the owner sends "GET /api/overview?tz=Mars/Olympus"
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "message": "不認識的時區 Mars/Olympus，請用 IANA 時區名稱，例如 Asia/Taipei。" }
      """

  @LIB-09 @LIB-R22
  Scenario: The overview reports disk usage without deleting anything
    Given "yt:bulk01xxxxx" has manual captions in "en"
    And the item "yt:bulk01xxxxx" has been processed up to "transcript"
    And the media of "yt:bulk01xxxxx" is cached
    When the owner sends "GET /api/overview"
    Then the response contains:
      """
      { "disk": { "data_bytes": "*", "media_bytes": "*", "artifacts_bytes": "*", "uploads_bytes": "*",
                  "free_bytes": "*", "measured_at": "*" } }
      """
    And the library contains "yt:bulk01xxxxx"

  @LIB-09 @LIB-R21 @LIB-R23 @ui @manual
  Scenario: The overview puts what needs the owner first
    Given two items are processing, one step is blocked by the budget and three events happened today
    When the owner opens "/"
    Then "現在" lists the two items with their compact time bands
    And "需要你處理" shows the blocked step with the buttons "調整預算" and "查看成本"
    And "今天" lists the events newest first
    And the bottom line reads "本月花費 $3.42，預算 $20" with "查看成本"
    And the "需要你處理" block is hidden once nothing needs the owner

  @LIB-08 @LIB-R14 @LIB-R18 @LIB-R23 @ui @manual
  Scenario: The bulk bar confirms with the combined impact before deleting
    Given the owner selected three items in the library
    When the owner chooses "刪除" in the bulk bar
    Then the confirmation dialog shows "3 部影音", the bytes that will be freed and the amount already spent
    And it offers "同時刪除未修改過的輸出檔（N 個）"
    When the owner confirms and one item fails
    Then a message lists the failed item and its reason, and the other two are in the trash
