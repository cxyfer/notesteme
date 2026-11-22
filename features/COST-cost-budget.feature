@M4
Feature: Cost ledger and budget
  As the owner, I want every API call recorded with its cost, a monthly budget and a per-item cap,
  and prices I can override, so that I know where my money goes and costs never run away.

  Background:
    Given the owner is signed in
    And the clock is at "2026-11-23T10:00:00Z"
    And the price of "anthropic" for "fixture-model" is "2.00" USD per "input_mtok"
    And the price of "anthropic" for "fixture-model" is "10.00" USD per "output_mtok"
    And the LLM usage reported by "anthropic" is 10000 input and 2000 output tokens
    And the profile "cost-correct" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default }
      """
    And the default profile is "cost-correct"
    And a YouTube video "yt:cost01xxxxx" titled "Kafka Streams at Scale"

  # 每次呼叫 10000 個輸入 token × $2／百萬 + 2000 個輸出 token × $10／百萬 = $0.04。

  @COST-01 @COST-R1 @COST-R2
  Scenario: A failed validation is recorded and charged like any other call
    Given the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the LLM provider "anthropic" answers "correct" with "correct-wrong-count" and then "correct-kafka-kraft"
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then the cost ledger entries of "yt:cost01xxxxx" are:
      | stage   | provider  | model         | attempt | outcome        | state  | usd  | unit_type | price_source |
      | correct | anthropic | fixture-model | 1       | invalid_output | actual | 0.04 | tokens    | override     |
      | correct | anthropic | fixture-model | 2       | ok             | actual | 0.04 | tokens    | override     |

  @COST-01 @COST-R1
  Scenario: A retried server error is recorded at no cost
    Given the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the LLM provider "anthropic" fails with HTTP 503 once
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then the cost ledger entries of "yt:cost01xxxxx" are:
      | attempt | outcome    | state  | usd  |
      | 1       | http_error | actual | 0    |
      | 2       | ok         | actual | 0.04 |

  @COST-01 @COST-R1
  Scenario: Each entry keeps the price it was charged at
    Given the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    And the owner sends "PUT /api/prices" with:
      """
      { "provider": "anthropic", "model": "fixture-model", "unit_type": "input_mtok", "usd_per_unit": 4.00 }
      """
    And the owner sends "GET /api/costs/entries?from=2026-11-01&to=2026-11-30"
    Then the response status is 200
    And the response contains:
      """
      { "entries": [ { "item_title": "Kafka Streams at Scale", "stage": "correct", "usd": 0.04,
                       "unit_price": { "source": "override", "input_mtok": 2.0, "output_mtok": 10.0,
                                       "input_tokens": 10000, "output_tokens": 2000 } } ] }
      """

  @COST-01 @COST-R2
  Scenario: An amount reported by the provider wins over the price list
    Given the price of "openai" for "fixture-model" is "2.00" USD per "input_mtok"
    And the price of "openai" for "fixture-model" is "10.00" USD per "output_mtok"
    And the profile "cost-openai" is:
      """
      stages:
        correct: { executor: api, provider: openai, model: fixture-model, prompt: correct-default }
      """
    And the LLM cost reported by "openai" is "0.0123" USD
    And the LLM provider "openai" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/yt:cost01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "cost-openai" }
      """
    And the worker runs until idle
    Then the cost ledger entries of "yt:cost01xxxxx" are:
      | provider | outcome | state  | usd    | price_source |
      | openai   | ok      | actual | 0.0123 | provider     |

  @COST-01 @COST-R2 @COST-R7
  Scenario: A model without a price is recorded as unknown when no budget is set
    Given the profile "cost-mystery" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: mystery-model, prompt: correct-default }
      """
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/yt:cost01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "cost-mystery" }
      """
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is "done"
    And the cost ledger entries of "yt:cost01xxxxx" are:
      | model         | state   | usd | price_source |
      | mystery-model | unknown | 0   | unknown      |

  @COST-02 @COST-R7 @COST-R8
  Scenario: A model without a price is blocked once a budget is set
    Given the budget is:
      | field       | value |
      | monthly_usd | 20    |
    And the profile "cost-mystery" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: mystery-model, prompt: correct-default }
      """
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/yt:cost01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "cost-mystery" }
      """
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is blocked with the reason "budget_blocked"
    And "yt:cost01xxxxx" shows the message "找不到 anthropic／mystery-model 的單價，無法檢查預算。"
    And "yt:cost01xxxxx" offers the actions "設定單價, 調整預算"
    And the LLM provider "anthropic" received 0 requests

  @COST-01 @COST-R3
  Scenario: Work done by an agent is unknown and never counts against the budget
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.01  |
    And the profile "cost-agent" is:
      """
      stages:
        correct: { executor: agent, provider: anthropic, model: fixture-model, prompt: correct-default }
      agent:
        max_claims: 4
        lease: 10m
        timeout: 24h
        on_timeout: fallback_api
      """
    And a token "claude-code" with scopes "read,agent"
    And an agent "claude" using the token "claude-code"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner sends "POST /api/items/yt:cost01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "cost-agent" }
      """
    And the worker runs until idle
    And the agent "claude" claims up to 4 tasks of kind "correct"
    And the agent "claude" submits "correct-kafka-kraft" for all its tasks
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is "done"
    And the cost ledger entries of "yt:cost01xxxxx" are:
      | stage   | state   | usd |
      | correct | unknown | 0   |
    When the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "spent_usd": 0, "reserved_usd": 0 }
      """

  @COST-02 @COST-R4 @COST-R11
  Scenario: The budget section replaces every field and reports this month
    Given the budget is:
      | field         | value |
      | per_item_usd  | 1     |
    When the owner sends "PUT /api/settings/budget" with:
      """
      { "monthly_usd": 20, "timezone": "Asia/Taipei" }
      """
    Then the response status is 200
    When the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "monthly_usd": 20, "per_item_usd": null, "warn_at_percent": 80, "timezone": "Asia/Taipei",
        "month": "2026-11", "spent_usd": 0, "reserved_usd": 0, "remaining_usd": 20 }
      """

  @COST-02 @COST-R4
  Scenario Outline: An invalid budget is rejected with the field at fault
    Given the budget is:
      | field       | value |
      | monthly_usd | 20    |
    When the owner sends "PUT /api/settings/budget" with:
      """
      <body>
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "errors": [ { "path": "<path>", "message": "<message>" } ] }
      """

    Examples:
      | body                          | path            | message                                                  |
      | { "monthly_usd": -1 }         | monthly_usd     | 月預算必須介於 0 到 100000 美元之間。                    |
      | { "per_item_usd": 100001 }    | per_item_usd    | 單部上限必須介於 0 到 100000 美元之間。                  |
      | { "warn_at_percent": 0 }      | warn_at_percent | 提醒門檻必須是 1 到 100 的整數。                         |
      | { "timezone": "Mars/Olympus" } | timezone        | 不認識的時區 Mars/Olympus，請用 IANA 時區名稱，例如 Asia/Taipei。 |
      | { "currency": "TWD" }         | currency        | 不認識的欄位。                                           |

  @COST-02 @COST-R4
  Scenario: Changing the budget needs the admin scope
    Given the budget is:
      | field       | value |
      | monthly_usd | 20    |
    And a token "viewer" with scopes "read"
    When the client sends "PUT /api/settings/budget" with the token "viewer" and:
      """
      { "monthly_usd": 1000 }
      """
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """

  @COST-02 @COST-R5 @COST-R6 @COST-R8
  Scenario: A step that would go over the monthly budget is blocked before any call
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.05  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the cost ledger has an entry of "0.03" USD for "yt:cost01xxxxx"
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is blocked with the reason "budget_blocked"
    And "yt:cost01xxxxx" shows the message "本月預算 $0.05 已用 $0.03（含預留），這個步驟預估 $0.04，超過預算。"
    And "yt:cost01xxxxx" offers the actions "調整預算, 查看成本"
    And the LLM provider "anthropic" received 0 requests

  @COST-02 @COST-R6 @COST-R8
  Scenario: A step that would go over the per-item cap is blocked
    Given the budget is:
      | field        | value |
      | per_item_usd | 0.05  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the cost ledger has an entry of "0.03" USD for "yt:cost01xxxxx" at "2026-09-01T00:00:00Z"
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is blocked with the reason "budget_blocked"
    And "yt:cost01xxxxx" shows the message "這部影音已花費 $0.03，這個步驟預估 $0.04，超過單部上限 $0.05。"

  @COST-02 @COST-R6
  Scenario: Reaching the budget exactly is not going over it
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.07  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the cost ledger has an entry of "0.03" USD for "yt:cost01xxxxx"
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is "done"

  @COST-02 @COST-R5
  Scenario: The reservation becomes the actual amount when the task finishes
    Given the budget is:
      | field       | value |
      | monthly_usd | 1     |
    And the cost estimate of every "correct" task is "0.06" USD
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then the cost ledger entries of "yt:cost01xxxxx" are:
      | stage   | state  | usd  |
      | correct | actual | 0.04 |
    When the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "spent_usd": 0.04, "reserved_usd": 0, "remaining_usd": 0.96 }
      """

  @COST-02 @COST-R5 @COST-R6
  Scenario: Parallel tasks never spend past the budget together
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.10  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the concurrency limit of "llm" is 3
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And a YouTube video "yt:cost02xxxxx" titled "Second talk"
    And a YouTube video "yt:cost03xxxxx" titled "Third talk"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the "segmented" transcript of "yt:cost02xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the "segmented" transcript of "yt:cost03xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the owner reruns "yt:cost02xxxxx" from "correct"
    And the owner reruns "yt:cost03xxxxx" from "correct"
    And the worker starts one round
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is "done"
    And the stage "correct" of "yt:cost02xxxxx" is "done"
    And the stage "correct" of "yt:cost03xxxxx" is blocked with the reason "budget_blocked"
    And the LLM provider "anthropic" received 2 requests for "correct"
    When the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "spent_usd": 0.08, "reserved_usd": 0 }
      """

  @COST-02 @COST-R5
  Scenario: A reservation left by a crash is released on restart
    Given the budget is:
      | field       | value |
      | monthly_usd | 1     |
    And the item "yt:cost01xxxxx" has been processed up to "metadata"
    And the cost ledger has a reservation of "0.50" USD for "yt:cost01xxxxx"
    When the server restarts
    And the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "reserved_usd": 0, "remaining_usd": 1 }
      """

  @COST-02 @COST-R9
  Scenario: Raising the budget lets a blocked step continue
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.05  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the cost ledger has an entry of "0.03" USD for "yt:cost01xxxxx"
    And the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    When the owner sends "PUT /api/settings/budget" with:
      """
      { "monthly_usd": 1 }
      """
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is "done"

  @COST-02 @COST-R9 @COST-R11
  Scenario: A new month in the budget time zone lets a blocked step continue
    Given the budget is:
      | field       | value       |
      | monthly_usd | 0.05        |
      | timezone    | Asia/Taipei |
    And the cost estimate of every "correct" task is "0.04" USD
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the clock advances by "173h"
    And the cost ledger has an entry of "0.03" USD for "yt:cost01xxxxx"
    And the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    And the stage "correct" of "yt:cost01xxxxx" is blocked with the reason "budget_blocked"
    # 2026-11-30T15:00Z 是台北的 23:00；再過 2 小時是台北的 12 月 1 日。
    When the clock advances by "2h"
    And the worker runs until idle
    Then the stage "correct" of "yt:cost01xxxxx" is "done"

  @COST-02 @COST-R11
  Scenario: The month follows the budget time zone
    Given the budget is:
      | field       | value       |
      | monthly_usd | 20          |
      | timezone    | Asia/Taipei |
    And the item "yt:cost01xxxxx" has been processed up to "metadata"
    And the cost ledger has an entry of "0.10" USD for "yt:cost01xxxxx" at "2026-10-31T17:00:00Z"
    And the cost ledger has an entry of "0.20" USD for "yt:cost01xxxxx" at "2026-11-22T12:00:00Z"
    When the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "month": "2026-11", "spent_usd": 0.30 }
      """
    When the owner sends "PUT /api/settings/budget" with:
      """
      { "monthly_usd": 20, "timezone": "UTC" }
      """
    And the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "month": "2026-11", "spent_usd": 0.20 }
      """

  @COST-02 @COST-R10
  Scenario: Crossing the warning threshold sends one budget warning
    Given the budget is:
      | field           | value |
      | monthly_usd     | 0.10  |
      | warn_at_percent | 80    |
    And the cost estimate of every "correct" task is "0.04" USD
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    And the "segmented" transcript of "yt:cost01xxxxx" is:
      | # | start    | end      | text                    |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled |
    And the cost ledger has an entry of "0.05" USD for "yt:cost01xxxxx"
    And the owner is listening to events
    When the owner reruns "yt:cost01xxxxx" from "correct"
    And the worker runs until idle
    Then an event is received:
      """
      { "type": "budget.warning", "month": "2026-11", "percent": 90, "monthly_usd": 0.10 }
      """

  @COST-03 @COST-R12
  Scenario: A price override is listed and can be removed
    When the owner sends "PUT /api/prices" with:
      """
      { "provider": "anthropic", "model": "fixture-model", "unit_type": "input_mtok", "usd_per_unit": 3.00 }
      """
    Then the response status is 200
    When the owner sends "GET /api/prices"
    Then the response contains:
      """
      { "prices": [ { "provider": "anthropic", "model": "fixture-model", "unit_type": "input_mtok",
                      "usd_per_unit": 3.00, "source": "override", "checked_at": "2026-11-23", "stale": false } ] }
      """
    When the owner sends "DELETE /api/prices" with:
      """
      { "provider": "anthropic", "model": "fixture-model", "unit_type": "input_mtok" }
      """
    Then the response status is 200
    When the owner sends "DELETE /api/prices" with:
      """
      { "provider": "anthropic", "model": "fixture-model", "unit_type": "input_mtok" }
      """
    Then the response status is 404

  @COST-03 @COST-R12
  Scenario Outline: An invalid price override is rejected with the field at fault
    When the owner sends "PUT /api/prices" with:
      """
      <body>
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "errors": [ { "path": "<path>", "message": "<message>" } ] }
      """

    Examples:
      | body                                                                                                | path         | message                                                  |
      | { "provider": "anthropic", "model": "m", "unit_type": "per_request", "usd_per_unit": 1 }            | unit_type    | 單位只能是 input_mtok、output_mtok、audio_hour 或 credit。 |
      | { "provider": "anthropic", "model": "m", "unit_type": "input_mtok", "usd_per_unit": -0.5 }          | usd_per_unit | 單價必須介於 0 到 10000 美元之間。                       |
      | { "provider": "", "model": "m", "unit_type": "input_mtok", "usd_per_unit": 1 }                      | provider     | 名稱必須是 1 到 200 個字元。                             |
      | { "provider": "anthropic", "model": "m", "unit_type": "input_mtok", "usd_per_unit": 1, "checked_at": "2026-12-01" } | checked_at   | 確認日期不能晚於今天。                                   |

  @COST-03 @COST-R12
  Scenario: Changing prices needs the admin scope
    Given a token "viewer" with scopes "read"
    When the client sends "PUT /api/prices" with the token "viewer" and:
      """
      { "provider": "anthropic", "model": "fixture-model", "unit_type": "input_mtok", "usd_per_unit": 0 }
      """
    Then the response status is 403
    When the client sends "GET /api/prices" with the token "viewer"
    Then the response status is 200

  @COST-03 @COST-R13
  Scenario: Stale built-in prices are pointed out until they are confirmed
    Given the built-in prices were checked on "2026-07-01"
    When the owner sends "GET /api/costs"
    Then the response contains:
      """
      { "notices": [ { "code": "prices_stale", "message": "計價表已經 145 天沒有確認，估價可能不準確。",
                       "actions": ["確認計價表", "調整單價"] } ] }
      """
    When the owner sends "POST /api/prices/confirm"
    And the owner sends "GET /api/prices"
    Then the response contains:
      """
      { "builtin_checked_at": "2026-11-23", "builtin_stale": false }
      """

  @COST-01 @COST-R14
  Scenario: Spending is grouped by day within the requested range
    Given the item "yt:cost01xxxxx" has been processed up to "metadata"
    And the cost ledger has an entry of "0.10" USD for "yt:cost01xxxxx" at "2026-11-02T08:00:00Z"
    And the cost ledger has an entry of "0.20" USD for "yt:cost01xxxxx" at "2026-11-05T08:00:00Z"
    And the cost ledger has an entry of "0.40" USD for "yt:cost01xxxxx" at "2026-10-30T08:00:00Z"
    When the owner sends "GET /api/costs?group_by=day&from=2026-11-01&to=2026-11-30"
    Then the response status is 200
    And the response contains:
      """
      { "from": "2026-11-01", "to": "2026-11-30", "timezone": "UTC", "total_usd": 0.30,
        "groups": [ { "key": "2026-11-02", "usd": 0.10, "calls": 1 },
                    { "key": "2026-11-05", "usd": 0.20, "calls": 1 } ] }
      """

  @COST-01 @COST-R14
  Scenario Outline: An invalid cost query is rejected
    When the owner sends "GET /api/costs?<query>"
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "message": "<message>" }
      """

    Examples:
      | query                              | message                                                  |
      | from=2026/11/01                    | 日期必須是 YYYY-MM-DD。                                  |
      | from=2026-11-10&to=2026-11-01      | 結束日期不能早於開始日期。                               |
      | from=2025-01-01&to=2026-11-23      | 查詢範圍最多 366 天。                                    |
      | group_by=weekday                   | group_by 只能是 day、provider、model、stage、channel 或 item。 |

  @COST-01 @COST-R15
  Scenario Outline: Exported titles cannot run as spreadsheet formulas
    Given a YouTube video "yt:csv01xxxxxx" titled "<title>"
    And the item "yt:csv01xxxxxx" has been processed up to "metadata"
    And the cost ledger has an entry of "0.10" USD for "yt:csv01xxxxxx"
    When the owner sends "GET /api/costs/export.csv?from=2026-11-01&to=2026-11-30"
    Then the response status is 200
    And the response header "Content-Type" contains "text/csv"
    And the response header "Content-Disposition" contains "notesteme-costs-2026-11-01-2026-11-30.csv"
    And the cost export has a row with "item_title" set to "<exported>"
    And the cost export has a row with "usd" set to "0.10"

    Examples:
      | title           | exported         |
      | =SUM(1+2) trick | '=SUM(1+2) trick |
      | +cmd hack       | '+cmd hack       |
      | -2+3 maths      | '-2+3 maths      |
      | @SUM(A1) talk   | '@SUM(A1) talk   |
      | Plain title     | Plain title      |

  @COST-01 @COST-02 @COST-R16 @ui @manual
  Scenario: The cost page shows this month against the budget
    Given the monthly budget is $20 and $3.42 has been spent this month
    When the owner opens the cost page
    Then the top line reads "本月花費 $3.42，預算 $20.00，預計月底約 $9.80。" followed by "調整預算"
    And the chart below can be grouped by provider, channel or stage
    And the table lists date, item, stage, provider, model, usage and amount, and exports CSV
    And no big-number stat cards are shown

  @COST-03 @COST-R13 @COST-R16 @ui @manual
  Scenario: Stale prices are shown at the top of the cost page
    Given the built-in prices were last confirmed 145 days ago
    When the owner opens the cost page
    Then a notice reads "計價表已經 145 天沒有確認，估價可能不準確。" with the actions "確認計價表" and "調整單價"
