@M2 @mcp
Feature: Agent tasks
  As an agent, I want to claim LLM tasks in batches with all the context I need
  and submit results that are validated like API results; as the owner, I want
  abandoned or unclaimed tasks to recover on their own.

  Background:
    Given the owner is signed in
    And the clock is at "2026-11-12T10:00:00Z"
    And the profile "agent-correct" is:
      """
      stages:
        correct: { executor: agent, prompt: correct-default, provider: anthropic, model: fixture-model }
      agent:
        max_claims: 4
        lease: 10m
        timeout: 24h
        on_timeout: fallback_api
      llm:
        chunk: { max_tokens: 4000 }
      """
    And the default profile is "agent-correct"
    And a token "claude-code" with scopes "read,agent"
    And a token "codex" with scopes "read,agent"
    And an agent "claude" using the token "claude-code"
    And an agent "codex" using the token "codex"
    And a YouTube video "yt:long08" titled "A very long talk (8 chunks)"
    And the item "yt:long08" has been processed up to "segment"
    And the owner reruns "yt:long08" from "correct"
    And the worker runs until idle

  @AGT-01 @AGT-R1 @AGT-R2
  Scenario: An agent claims several tasks with their full context
    Then the stage "correct" of "yt:long08" is "awaiting_agent"
    When the agent "claude" claims up to 10 tasks of kind "correct"
    Then the agent "claude" holds 4 tasks
    And the tool result contains:
      """
      { "tasks": [ { "lease_token": "*", "lease_until": "2026-11-12T10:10:00Z",
                     "prompt": "*", "cues": "*", "output_schema": "*" } ] }
      """

  @AGT-02 @AGT-R3
  Scenario: Two agents never receive the same task
    When the agent "claude" claims up to 3 tasks of kind "correct"
    And the agent "codex" claims up to 3 tasks of kind "correct"
    Then the agent "claude" holds 3 tasks
    And the agent "codex" holds 3 tasks
    And no task is held by both "claude" and "codex"

  @AGT-01 @AGT-R5 @AGT-R10
  Scenario: A valid submission completes the task
    When the agent "claude" claims up to 1 task of kind "correct"
    And the agent "claude" submits "correct-echo" for its task 1
    Then the submission is accepted
    And the agent "claude" holds 0 tasks

  @AGT-03 @AGT-R5
  Scenario: An invalid submission is explained and the lease is kept
    When the agent "claude" claims up to 1 task of kind "correct"
    And the agent "claude" submits "correct-wrong-count" for its task 1
    Then the submission is rejected with "cue_count"
    And the agent "claude" holds 1 task
    When the agent "claude" submits "correct-echo" for its task 1
    Then the submission is accepted

  @AGT-03 @AGT-R6
  Scenario: Three failed submissions return the task to the queue
    When the agent "claude" claims up to 1 task of kind "correct"
    And the agent "claude" submits "correct-wrong-count" for its task 1
    And the agent "claude" submits "correct-wrong-count" for its task 1
    And the agent "claude" submits "correct-wrong-count" for its task 1
    Then the agent "claude" holds 0 tasks
    And the stage "correct" of "yt:long08" is "awaiting_agent"

  @AGT-04 @AGT-R4
  Scenario: An expired lease returns the task to the queue
    When the agent "claude" claims up to 1 task of kind "correct"
    And the clock advances by "11m"
    And the worker runs until idle
    Then the agent "claude" holds 0 tasks
    When the agent "codex" claims up to 10 tasks of kind "correct"
    Then the agent "codex" holds 4 tasks

  @AGT-04 @AGT-R4
  Scenario: Extending the lease keeps the task
    When the agent "claude" claims up to 1 task of kind "correct"
    And the clock advances by "8m"
    And the agent "claude" extends the lease of its task 1
    And the clock advances by "8m"
    And the worker runs until idle
    Then the agent "claude" holds 1 task

  @AGT-04 @AGT-R7
  Scenario: A stale lease token cannot submit
    When the agent "claude" claims up to 1 task of kind "correct"
    And the clock advances by "11m"
    And the worker runs until idle
    And the agent "codex" claims up to 4 tasks of kind "correct"
    And the agent "claude" submits "correct-echo" for its task 1
    Then the submission is rejected with "lease_expired"

  @AGT-04 @AGT-R8
  Scenario: Releasing a task puts it back immediately
    When the agent "claude" claims up to 1 task of kind "correct"
    And the agent "claude" releases its task 1 with the reason "out of context window"
    And the agent "codex" claims up to 10 tasks of kind "correct"
    Then the agent "codex" holds 4 tasks

  @AGT-05 @AGT-R9
  Scenario: Unclaimed tasks fall back to the API after the timeout
    Given the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the clock advances by "25h"
    And the worker runs until idle
    Then the stage "correct" of "yt:long08" is "done"
    And the "corrected" artifact of "yt:long08" has the provenance:
      | field    | value     |
      | executor | api       |
      | provider | anthropic |

  @AGT-05 @AGT-R9
  Scenario: With the notify policy unclaimed tasks ask the owner
    Given the profile "agent-notify" is:
      """
      stages:
        correct: { executor: agent, prompt: correct-default }
      agent: { timeout: 24h, on_timeout: notify }
      """
    And the owner sends "POST /api/items/yt:long08/rerun" with:
      """
      { "from": "correct", "profile": "agent-notify" }
      """
    And the worker runs until idle
    When the clock advances by "25h"
    And the worker runs until idle
    Then the stage "correct" of "yt:long08" is blocked with the reason "needs_action"
    And "yt:long08" shows the message "這個任務等待 Agent 超過 24 小時。"
    And "yt:long08" offers the actions "改用 API 處理"

  @AGT-06 @AGT-R10
  Scenario: Agent results are marked as agent-reported
    When the agent "claude" claims up to 10 tasks of kind "correct"
    And the agent "claude" submits "correct-echo-reported-model" for all its tasks
    And the agent "claude" claims up to 10 tasks of kind "correct"
    And the agent "claude" submits "correct-echo-reported-model" for all its tasks
    And the worker runs until idle
    Then the "corrected" artifact of "yt:long08" has the provenance:
      | field          | value |
      | executor       | agent         |
      | agent          | claude-code   |
      | model_reported | fixture-model |
      | model_verified | false         |

  @AGT-05 @AGT-R11
  Scenario: The owner switches a waiting task to the API
    Given the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the agent "claude" claims up to 1 task of kind "correct"
    And the owner sends "POST /api/items/yt:long08/switch-executor" with:
      """
      { "stage": "correct", "executor": "api" }
      """
    And the worker runs until idle
    And the agent "claude" submits "correct-echo" for its task 1
    Then the submission is rejected with "lease_expired"
    And the stage "correct" of "yt:long08" is "done"

  @AGT-01 @AGT-R12
  Scenario: A token without the agent scope cannot claim
    Given a token "reader" with scopes "read"
    And an agent "intruder" using the token "reader"
    When the agent "intruder" claims up to 1 task of kind "correct"
    Then the tool call fails with "agent"
