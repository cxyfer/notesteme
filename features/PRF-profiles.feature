@M2
Feature: Profiles and layered settings
  As the owner, I want reusable profiles and a fixed order for global, profile,
  channel and per-run settings, so that I can predict what will run.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:talk01" titled "Kafka talk"

  @PRF-02 @PRF-R2
  Scenario: Each layer overrides the one before it
    Given the global settings are:
      """
      language: { targets: [zh-Hant-TW], fallback: [supadata, groq] }
      timing: { split_timing: when_needed }
      """
    And the profile "standard" is:
      """
      timing: { split_timing: forbid }
      """
    And the channel of "yt:talk01" has the overrides:
      """
      language: { targets: [ja] }
      """
    When the owner ingests "yt:talk01" with the overrides:
      """
      language: { fallback: [groq] }
      """
    Then the effective config of "yt:talk01" for "transcript" contains:
      """
      language: { targets: [ja], fallback: [groq] }
      timing: { split_timing: forbid }
      """

  @PRF-02 @PRF-R2
  Scenario: Lists are replaced, not merged
    Given the global settings are:
      """
      language: { targets: [zh-Hant-TW, en] }
      """
    When the owner ingests "yt:talk01" with the overrides:
      """
      language: { targets: [ja] }
      """
    Then the effective config of "yt:talk01" for "translate" contains:
      """
      language: { targets: [ja] }
      """

  @PRF-01 @PRF-R3
  Scenario: Editing a profile does not touch finished work
    Given the profile "standard" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default }
      """
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    And the item "yt:talk01" has been processed up to "correct"
    When the owner sends "PATCH /api/profiles/standard" with:
      """
      { "config": { "stages": { "correct": { "model": "fixture-model-2" } } } }
      """
    Then the "corrected" artifact of "yt:talk01" has 1 version
    And the response contains:
      """
      { "version": 2 }
      """

  @PRF-01 @PRF-R1
  Scenario: A complete profile is accepted
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "full", "config": {
          "stages": {
            "segment":   { "executor": "api", "provider": "openai", "model": "fixture-model", "prompt": "segment-default" },
            "correct":   { "executor": "agent", "prompt": "correct-default" },
            "translate": { "executor": "opencc" } },
          "language": { "targets": ["zh-Hant-TW"], "variant_strategy": "convert" },
          "timing": { "split_timing": "when_needed", "split_timing_audio": "auto" },
          "llm": { "chunk": { "max_tokens": 4000, "overlap_cues": 3, "context_cues": 5 } },
          "agent": { "timeout": "24h", "on_timeout": "fallback_api" },
          "subtitle_rules": { "cjk": { "max_chars_per_line": 22 } },
          "outputs": [ { "target": "sidecar", "formats": ["srt"], "lang": ["zh-Hant-TW"] } ] } }
      """
    Then the response status is 201
    And the response contains:
      """
      { "name": "full", "version": 1 }
      """

  @PRF-04 @PRF-R4
  Scenario Outline: Invalid profiles are rejected with the field path
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "broken", "config": <config> }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "<path>" } ] }
      """

    Examples:
      | config                                                                  | path                       |
      | { "stages": { "correct": { "executor": "api" } } }                       | stages.correct.provider    |
      | { "stages": { "correct": { "executor": "opencc" } } }                    | stages.correct.executor    |
      | { "language": { "targets": "en" } }                                      | language.targets           |
      | { "stages": { "correct": { "executor": "api", "provider": "nope", "model": "m" } } } | stages.correct.provider |
      | { "stages": { "correct": { "executor": "api", "provider": "anthropic", "model": "m", "prompt": "missing" } } } | stages.correct.prompt |
      | { "colour": "blue" }                                                     | colour                     |

  @PRF-03 @PRF-R5 @PRF-R6
  Scenario: Executors can be mixed per stage
    Given the profile "mixed" is:
      """
      stages:
        segment:   { executor: off }
        correct:   { executor: agent, prompt: correct-default }
        translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
      """
    And the item "yt:talk01" has been processed up to "normalize"
    When the owner sends "POST /api/items/yt:talk01/rerun" with:
      """
      { "from": "segment", "profile": "mixed" }
      """
    And the worker runs until idle
    Then the stage "segment" of "yt:talk01" is "skipped"
    And the stage "correct" of "yt:talk01" is "awaiting_agent"

  @PRF-01 @PRF-R7
  Scenario: The built-in default profile cannot be deleted
    When the owner sends "DELETE /api/profiles/default"
    Then the response status is 409

  @PRF-02 @PRF-R8
  Scenario: Channel overrides apply to later items of that channel
    Given a YouTube video "yt:talk02" titled "Kafka talk part 2"
    And the channel of "yt:talk01" has the overrides:
      """
      language: { targets: [ja] }
      """
    When the owner ingests "yt:talk02"
    Then the effective config of "yt:talk02" for "translate" contains:
      """
      language: { targets: [ja] }
      """
