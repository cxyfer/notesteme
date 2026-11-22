@M4
Feature: Batch API for background work
  As the owner, I want non-urgent LLM work sent through provider batch APIs,
  so that background processing costs less.

  Background:
    Given the owner is signed in
    And the clock is at "2026-11-23T10:00:00Z"
    And the batch API of "anthropic" completes after "10m"
    And the price of "anthropic" for "fixture-model" is "2.00" USD per "input_mtok"
    And the price of "anthropic" for "fixture-model" is "10.00" USD per "output_mtok"
    And the LLM usage reported by "anthropic" is 10000 input and 2000 output tokens
    And the profile "batch-auto" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default, batch: auto }
      """
    And the default profile is "batch-auto"
    And a YouTube video "yt:batch1xxxxx" titled "Kafka Streams at Scale"
    And "yt:batch1xxxxx" has manual captions in "en"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"

  # 預設的 120 句字幕是一個 chunk；一般單價每次呼叫 $0.04，batch 半價 $0.02。

  @BATCH-01 @BATCH-R1 @BATCH-R2 @BATCH-R3
  Scenario: Subscription work is sent as a batch and waits for the result
    Given the item "yt:batch1xxxxx" has been ingested as subscription
    When the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "waiting"
    And "yt:batch1xxxxx" shows the message "已送出 Batch API，等待結果，最晚 2026-11-24 10:00 UTC。"
    And the batch count of "anthropic" is 1
    And the batch request count of "anthropic" is 1
    And the LLM provider "anthropic" received 0 requests for "correct"

  @BATCH-01 @BATCH-R4 @BATCH-R8
  Scenario: The batch result completes the stage on the next poll
    Given the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the clock advances by "15m"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the LLM provider "anthropic" received 0 requests for "correct"
    And the "corrected" artifact of "yt:batch1xxxxx" has the provenance:
      | field           | value |
      | batch           | true  |
      | chunks.0.mode   | batch |
      | chunks.0.batch_id | *   |

  @BATCH-01 @BATCH-R1
  Scenario: Work the owner added by hand never waits for a batch
    When the owner ingests "yt:batch1xxxxx"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the batch count of "anthropic" is 0

  @BATCH-01 @BATCH-R1
  Scenario: Batches are only used when the profile turns them on
    Given the channel of "yt:batch1xxxxx" has the overrides:
      """
      stages:
        correct: { batch: off }
      """
    And the item "yt:batch1xxxxx" has been ingested as backfill
    When the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the batch count of "anthropic" is 0

  @BATCH-01 @BATCH-R1
  Scenario Outline: Invalid batch settings are rejected with the field at fault
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "bad-batch", "config": { "stages": { "<stage>": { "batch": "<value>" } } } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "errors": [ { "path": "stages.<stage>.batch", "message": "<message>" } ] }
      """

    Examples:
      | stage      | value     | message                  |
      | correct    | sometimes | batch 只能是 auto 或 off。 |
      | transcript | auto      | 這個步驟不支援 batch。     |

  @BATCH-01 @BATCH-R2 @BATCH-R4
  Scenario: OpenAI batches go through an uploaded request file
    Given the batch API of "openai" completes after "10m"
    And the LLM provider "openai" answers "correct" with "correct-echo"
    And the channel of "yt:batch1xxxxx" has the overrides:
      """
      stages:
        correct: { provider: openai }
      """
    And the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    And the batch count of "openai" is 1
    When the clock advances by "15m"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the LLM provider "openai" received 0 requests for "correct"
    And the batch count of "anthropic" is 0

  @BATCH-01 @BATCH-R4 @BATCH-R8
  Scenario: A failed request in a batch falls back on its own
    Given the channel of "yt:batch1xxxxx" has the overrides:
      """
      llm:
        chunk: { max_tokens: 300 }
      """
    And the batch API of "anthropic" fails the requests for chunk 1
    And the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the clock advances by "15m"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the LLM provider "anthropic" received 1 request for "correct"
    And the "corrected" artifact of "yt:batch1xxxxx" has the provenance:
      | field                    | value          |
      | chunks.0.mode            | api            |
      | chunks.0.fallback_reason | request_failed |
      | chunks.1.mode            | batch          |

  @BATCH-01 @BATCH-R5 @BATCH-R8
  Scenario: A batch that takes too long is cancelled and the work goes to the regular API
    Given the batch API of "anthropic" never completes
    And the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the clock advances by "25h"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the cancelled batch count of "anthropic" is 1
    And the "corrected" artifact of "yt:batch1xxxxx" has the provenance:
      | field                    | value   |
      | chunks.0.mode            | api     |
      | chunks.0.fallback_reason | timeout |

  @BATCH-01 @BATCH-R6
  Scenario: Batch results are charged at the batch discount
    Given the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the clock advances by "15m"
    And the worker runs until idle
    Then the cost ledger entries of "yt:batch1xxxxx" are:
      | stage   | state  | usd  | batch | price_source |
      | correct | actual | 0.02 | true  | override     |

  @BATCH-01 @BATCH-R7
  Scenario: The reservation for a batch uses the batch price
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.03  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the item "yt:batch1xxxxx" has been ingested as subscription
    When the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "waiting"
    When the owner sends "GET /api/settings/budget"
    Then the response contains:
      """
      { "reserved_usd": 0.02 }
      """

  @BATCH-01 @BATCH-R5 @BATCH-R7
  Scenario: Falling back to the regular API checks the budget again at the full price
    Given the budget is:
      | field       | value |
      | monthly_usd | 0.03  |
    And the cost estimate of every "correct" task is "0.04" USD
    And the batch API of "anthropic" never completes
    And the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the clock advances by "25h"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is blocked with the reason "budget_blocked"
    And the LLM provider "anthropic" received 0 requests for "correct"

  @BATCH-01 @BATCH-R9
  Scenario: Deleting an item cancels the batch it was waiting for
    Given the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the owner sends "DELETE /api/items/yt:batch1xxxxx"
    And the worker runs until idle
    Then the cancelled batch count of "anthropic" is 1

  @BATCH-01 @BATCH-R9
  Scenario: A result that arrives after a cancellation is still recorded as spent
    Given the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    # batch 在 10 分鐘時已經在 provider 端完成，但還沒有被輪詢到。
    When the clock advances by "15m"
    And the owner reruns "yt:batch1xxxxx" from "correct"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the cost ledger entries of "yt:batch1xxxxx" are:
      | stage   | outcome   | state  | usd  | batch |
      | correct | discarded | actual | 0.02 | true  |
      | correct | ok        | actual | 0.04 | false |

  @BATCH-01 @BATCH-R10
  Scenario: Waiting batches survive a restart without being sent again
    Given the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the server restarts
    And the clock advances by "15m"
    And the worker runs until idle
    Then the stage "correct" of "yt:batch1xxxxx" is "done"
    And the batch count of "anthropic" is 1

  @BATCH-01 @BATCH-R11
  Scenario: Results on another host are never fetched
    Given the batch API of "anthropic" points its results to "https://results.evil.example/batch.jsonl"
    And the host "results.evil.example" resolves to "203.0.113.7"
    And the item "yt:batch1xxxxx" has been ingested as subscription
    And the worker runs until idle
    When the clock advances by "15m"
    And the worker runs until idle
    Then no connection was made to "results.evil.example"
    And the stage "correct" of "yt:batch1xxxxx" is "done"
    And the LLM provider "anthropic" received 1 request for "correct"
    And the "corrected" artifact of "yt:batch1xxxxx" has the provenance:
      | field                    | value        |
      | chunks.0.fallback_reason | batch_failed |
