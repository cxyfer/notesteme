@M2
Feature: Prompt library
  As the owner, I want versioned prompts I can try safely and promote on
  purpose, with variable mistakes caught when I save.

  Background:
    Given the owner is signed in
    And the prompt "my-correct" version 1 is:
      """
      你是字幕校對員。只修正錯字、專有名詞與標點。
      詞彙表：{{glossary}}
      """

  @PRM-01 @PRM-R1
  Scenario: A prompt must have a known kind
    When the owner sends "POST /api/prompts" with:
      """
      { "id": "my-poem", "kind": "poem", "body": "寫一首詩" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "kind" } ] }
      """

  @PRM-01 @PRM-R2
  Scenario: Saving a change creates a new version
    When the owner sends "POST /api/prompts/my-correct/versions" with:
      """
      { "body": "你是字幕校對員。保留口語語氣。\n詞彙表：{{glossary}}" }
      """
    Then the response status is 201
    And the response contains:
      """
      { "id": "my-correct", "version": 2 }
      """
    When the owner sends "GET /api/prompts/my-correct/versions"
    Then the response contains:
      """
      { "versions": [ { "version": 1 }, { "version": 2 } ] }
      """

  @PRM-04 @PRM-R3
  Scenario: An undefined variable is rejected on save
    When the owner sends "POST /api/prompts/my-correct/versions" with:
      """
      { "body": "標題：{{titel}}" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "undefined_variables": ["titel"] }
      """

  @PRM-01 @PRM-R4
  Scenario: A built-in prompt cannot be changed in place
    When the owner sends "POST /api/prompts/correct-default/versions" with:
      """
      { "body": "something else" }
      """
    Then the response status is 409

  @PRM-03 @PRM-R5
  Scenario: A profile keeps its pinned version after a new one is saved
    Given the profile "standard" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: my-correct@1 }
      """
    And a YouTube video "yt:talk01xxxxx" titled "Kafka talk"
    And the item "yt:talk01xxxxx" has been processed up to "segment"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the owner sends "POST /api/prompts/my-correct/versions" with:
      """
      { "body": "新的版本 {{glossary}}" }
      """
    And the owner sends "POST /api/items/yt:talk01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "standard" }
      """
    And the worker runs until idle
    Then the "corrected" artifact of "yt:talk01xxxxx" has the provenance:
      | field          | value      |
      | prompt.id      | my-correct |
      | prompt.version | 1          |

  @PRM-03 @PRM-R5
  Scenario: Promoting a version changes profiles that follow the default
    Given the profile "standard" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: my-correct@default }
      """
    And the prompt "my-correct" version 2 is:
      """
      新的版本 {{glossary}}
      """
    And a YouTube video "yt:talk01xxxxx" titled "Kafka talk"
    And the item "yt:talk01xxxxx" has been processed up to "segment"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the owner sends "POST /api/prompts/my-correct/default" with:
      """
      { "version": 2 }
      """
    And the owner sends "POST /api/items/yt:talk01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "standard" }
      """
    And the worker runs until idle
    Then the "corrected" artifact of "yt:talk01xxxxx" has the provenance:
      | field          | value |
      | prompt.version | 2     |

  @PRM-02 @PRM-R6
  Scenario: A playground run changes nothing
    Given a YouTube video "yt:talk01xxxxx" titled "Kafka talk"
    And the item "yt:talk01xxxxx" has been processed up to "correct"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the owner sends "POST /api/prompts/playground" with:
      """
      { "prompt": "my-correct@1", "item": "yt:talk01xxxxx", "from": "00:06:00", "to": "00:08:00",
        "provider": "anthropic", "model": "fixture-model" }
      """
    Then the response status is 200
    And the response contains:
      """
      { "validation": { "passed": true }, "estimate_usd": "*" }
      """
    And the "corrected" artifact of "yt:talk01xxxxx" has 1 version
    And the stage "correct" of "yt:talk01xxxxx" is "done"

  @PRM-02 @PRM-R7
  Scenario: Compare two versions on the same lines
    Given the prompt "my-correct" version 2 is:
      """
      新的版本 {{glossary}}
      """
    And a YouTube video "yt:talk01xxxxx" titled "Kafka talk"
    And the item "yt:talk01xxxxx" has been processed up to "segment"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the owner sends "POST /api/prompts/my-correct/compare" with:
      """
      { "a": 1, "b": 2, "item": "yt:talk01xxxxx", "from": "00:00:00", "to": "00:01:00", "provider": "anthropic", "model": "fixture-model" }
      """
    Then the response contains:
      """
      { "body_diff": "*", "outputs": { "a": "*", "b": "*" } }
      """

  @PRM-01 @PRM-R8
  Scenario: A prompt in use cannot be deleted
    Given the profile "standard" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: my-correct@1 }
      """
    When the owner sends "DELETE /api/prompts/my-correct"
    Then the response status is 409
    And the response contains:
      """
      { "used_by": ["standard"] }
      """
