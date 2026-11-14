@M1
Feature: Pipeline and task queue
  As the owner, I want every item processed stage by stage with clear statuses,
  automatic retries, reuse of finished work, and a queue I can control.

  Background:
    Given the owner is signed in
    And the clock is at "2026-11-12T10:00:00Z"
    And a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And "yt:abc123xxxxx" has no caption tracks

  @PIPE-01 @PIPE-R1
  Scenario: Processing stops after the requested stage
    Given the source "supadata" returns "supadata-en.json" for "yt:abc123xxxxx"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:abc123xxxxx" is "done"
    And the stage "normalize" of "yt:abc123xxxxx" is "pending"
    And no task is running

  @PIPE-02 @PIPE-R2 @PIPE-R3
  Scenario: A temporary failure is retried with backoff
    Given the source "supadata" returns "http-503" for "yt:abc123xxxxx"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      language:
        fallback: [supadata]
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:abc123xxxxx" retries at "2026-11-12T10:00:30Z"
    And the task of "transcript" for "yt:abc123xxxxx" has 1 attempt

  @PIPE-02 @PIPE-R3
  Scenario: A temporary failure becomes a failure after three attempts
    Given the source "supadata" returns "http-503" for "yt:abc123xxxxx"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      language:
        fallback: [supadata]
      until: transcript
      """
    And the worker runs until idle
    And the clock advances by "30s"
    And the worker runs until idle
    And the clock advances by "2m"
    And the worker runs until idle
    Then the stage "transcript" of "yt:abc123xxxxx" failed with the error class "retryable"
    And the task of "transcript" for "yt:abc123xxxxx" has 3 attempts

  @PIPE-02 @PIPE-R4
  Scenario: A blocked stage resumes after the fix action
    Given the source "bilibili-ai" requires the cookie "bilibili"
    And a Bilibili video "BV1ai411c7mD" with 1 part
    And "bili:BV1ai411c7mD/p1" has auto captions in "zh-Hans"
    And the item "bili:BV1ai411c7mD/p1" has been ingested
    And the source "bilibili-ai" returns "bilibili-ai-zh.json" for "bili:BV1ai411c7mD/p1"
    When the worker runs until idle
    Then the stage "transcript" of "bili:BV1ai411c7mD/p1" is blocked with the reason "needs_action"
    When the owner sends "POST /api/cookies" with:
      """
      { "platform": "bilibili", "content": "SESSDATA=fixture" }
      """
    And the worker runs until idle
    Then the stage "transcript" of "bili:BV1ai411c7mD/p1" is "done"
    And the original language of "bili:BV1ai411c7mD/p1" is "zh-Hans"

  @PIPE-03 @PIPE-R5
  Scenario: Rerunning with the same settings reuses the cached result
    Given the source "supadata" returns "supadata-en.json" for "yt:abc123xxxxx"
    And the item "yt:abc123xxxxx" has been processed up to "transcript"
    When the owner reruns "yt:abc123xxxxx" from "transcript"
    And the worker runs until idle
    Then the source "supadata" was called 1 time
    And the "source" artifact of "yt:abc123xxxxx" has 1 version

  @PIPE-03 @PIPE-R6
  Scenario: A new stage implementation invalidates the cache
    Given the "source" transcript of "yt:abc123xxxxx" is:
      | # | start    | end      | text  |
      | 1 | 00:00:01 | 00:00:03 | hello |
    And the item "yt:abc123xxxxx" has been processed up to "normalize"
    And the implementation version of the stage "normalize" is 2
    When the owner reruns "yt:abc123xxxxx" from "normalize"
    And the worker runs until idle
    Then the "normalized" artifact of "yt:abc123xxxxx" has 2 versions

  @PIPE-07 @PIPE-R7
  Scenario: Only the three most recent versions are kept
    Given the "source" transcript of "yt:abc123xxxxx" is:
      | # | start    | end      | text  |
      | 1 | 00:00:01 | 00:00:03 | hello |
    And the item "yt:abc123xxxxx" has been processed up to "normalize"
    And the implementation version of the stage "normalize" is 2
    When the owner reruns "yt:abc123xxxxx" from "normalize"
    And the worker runs until idle
    And the implementation version of the stage "normalize" is 3
    And the owner reruns "yt:abc123xxxxx" from "normalize"
    And the worker runs until idle
    And the implementation version of the stage "normalize" is 4
    And the owner reruns "yt:abc123xxxxx" from "normalize"
    And the worker runs until idle
    Then the "normalized" artifact of "yt:abc123xxxxx" has 3 versions

  @PIPE-04 @PIPE-R8
  Scenario: The concurrency limit caps running tasks
    Given the concurrency limit of "download" is 2
    And a YouTube video "yt:v1xxxxxxxxx" titled "one"
    And a YouTube video "yt:v2xxxxxxxxx" titled "two"
    And a YouTube video "yt:v3xxxxxxxxx" titled "three"
    And the item "yt:v1xxxxxxxxx" has been ingested
    And the item "yt:v2xxxxxxxxx" has been ingested
    And the item "yt:v3xxxxxxxxx" has been ingested
    When the worker starts one round
    Then 2 tasks of "download" are running

  @PIPE-04 @PIPE-R8
  Scenario: Raising the limit takes effect on the next round
    Given the concurrency limit of "download" is 1
    And a YouTube video "yt:v1xxxxxxxxx" titled "one"
    And a YouTube video "yt:v2xxxxxxxxx" titled "two"
    And the item "yt:v1xxxxxxxxx" has been ingested
    And the item "yt:v2xxxxxxxxx" has been ingested
    When the owner sends "PATCH /api/queue/limits" with:
      """
      { "download": 2 }
      """
    And the worker starts one round
    Then 2 tasks of "download" are running

  @PIPE-04 @PIPE-R10
  Scenario: A paused queue starts nothing new
    Given the item "yt:abc123xxxxx" has been ingested
    When the owner pauses the queue
    And the worker starts one round
    Then no task is running
    When the owner resumes the queue
    And the worker starts one round
    Then 1 task of "download" is running

  @PIPE-04 @PIPE-R10
  Scenario: Pausing one resource class leaves the others running
    Given the resource class "asr" is paused
    And the source "groq" returns "groq-en.json" for "yt:abc123xxxxx"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      language: { fallback: [groq] }
      until: transcript
      """
    And the worker runs until idle
    Then the stage "metadata" of "yt:abc123xxxxx" is "done"
    And the stage "transcript" of "yt:abc123xxxxx" is "pending"

  @PIPE-04 @PIPE-R10
  Scenario: Pausing the asr class does not hold a transcript from platform captions
    Given the resource class "asr" is paused
    And "yt:abc123xxxxx" has manual captions in "en"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      language: { fallback: [groq] }
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:abc123xxxxx" is "done"

  @PIPE-05 @PIPE-R9
  Scenario: Items added by hand run before backfill
    Given a YouTube video "yt:old001xxxxx" titled "Backfilled"
    And the item "yt:old001xxxxx" has been ingested as backfill
    And the concurrency limit of "download" is 1
    When the owner ingests "yt:abc123xxxxx"
    Then the next task belongs to "yt:abc123xxxxx"

  @PIPE-05 @PIPE-R9
  Scenario: A long-waiting backfill task eventually goes first
    Given a YouTube video "yt:old001xxxxx" titled "Backfilled"
    And the item "yt:old001xxxxx" has been ingested as backfill
    And the concurrency limit of "download" is 1
    When the clock advances by "3h"
    And the owner ingests "yt:abc123xxxxx"
    Then the next task belongs to "yt:old001xxxxx"

  @PIPE-06 @PIPE-R11
  Scenario: Leased tasks come back after a crash without using up attempts
    Given the source "supadata" returns "supadata-en.json" for "yt:abc123xxxxx"
    And the server is running
    And the item "yt:abc123xxxxx" has been ingested
    When the worker starts one round
    And the server restarts
    And the worker runs until idle
    Then the stage "metadata" of "yt:abc123xxxxx" is "done"
    And the task of "metadata" for "yt:abc123xxxxx" has 1 attempt
    And the file "/data/tmp/partial-download.part" does not exist

  @PIPE-06 @PIPE-R12
  Scenario: A graceful shutdown releases what it cannot finish
    Given the server is running
    And the item "yt:abc123xxxxx" has been ingested
    When the worker starts one round
    And the server shuts down gracefully
    Then the stage "metadata" of "yt:abc123xxxxx" is "pending"
    And no task is running

  @PIPE-07 @PIPE-R13
  Scenario: Each run records the settings it used
    Given the source "supadata" returns "supadata-en.json" for "yt:abc123xxxxx"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      language: { targets: [en], fallback: [supadata] }
      until: transcript
      """
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:abc123xxxxx/runs"
    Then the response contains:
      """
      { "runs": [ { "stage": "transcript", "config": { "language": { "targets": ["en"], "fallback": ["supadata"] } } } ] }
      """

  @PIPE-01 @PIPE-R14
  Scenario: Status changes are pushed as events
    Given the owner is listening to events
    When the owner ingests "yt:abc123xxxxx"
    Then an event is received:
      """
      { "type": "stage.status", "item": "yt:abc123xxxxx", "stage": "metadata", "status": "pending" }
      """

  @PIPE-04 @PIPE-R15
  Scenario: Cancelling an item stops its running work
    Given the item "yt:abc123xxxxx" has been ingested
    When the worker starts one round
    And the owner sends "POST /api/items/yt:abc123xxxxx/cancel"
    Then the stage "metadata" of "yt:abc123xxxxx" is "skipped"
    And no task is running
