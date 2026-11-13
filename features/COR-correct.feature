@M2
Feature: Correct subtitles line by line
  As the owner, I want typos, names and punctuation fixed one cue at a time,
  with anything that strays too far reverted to the original.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:talk01xxxxx" with:
      | field       | value                              |
      | title       | Kafka Streams at Scale             |
      | description | A talk about Apache Kafka and KRaft |
    And the profile "cor-test" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default, min_similarity: 0.6 }
      """
    And the default profile is "cor-test"
    And the "segmented" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams     |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled  |

  @COR-01 @COR-R1
  Scenario: Corrections keep the cue count and timings
    Given the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    When the owner reruns "yt:talk01xxxxx" from "correct"
    And the worker runs until idle
    Then the "corrected" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:03 | We use Kafka Streams.    |
      | 2 | 00:00:03 | 00:00:06 | With KRaft mode enabled. |

  @COR-02 @COR-R1
  Scenario: A correction that changes the number of cues is rejected
    Given the LLM provider "anthropic" answers "correct" with "correct-wrong-count" and then "correct-kafka-kraft"
    When the owner reruns "yt:talk01xxxxx" from "correct"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 2 requests for "correct"
    And the "corrected" transcript of "yt:talk01xxxxx" has 2 cues

  @COR-02 @COR-R2 @COR-R3 @pending-m0
  Scenario: A rewrite that strays too far falls back to the original
    Given the LLM provider "anthropic" answers "correct" with "correct-rewrites-meaning"
    When the owner reruns "yt:talk01xxxxx" from "correct"
    And the worker runs until idle
    Then the "corrected" transcript of "yt:talk01xxxxx" is:
      | # | text                    |
      | 1 | we use cafca streams    |
      | 2 | with craft mode enabled |
    And "yt:talk01xxxxx" shows the message "第 1 段的校正結果和原文差異過大（相似度 0.42），已退回原文。"
    And "yt:talk01xxxxx" offers the actions "重新校正這段, 開啟 Prompt"

  @COR-03 @COR-R4
  Scenario: Channel glossary and video metadata reach the prompt
    Given the channel of "yt:talk01xxxxx" has the overrides:
      """
      glossary: [KRaft, Confluent]
      """
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    When the owner reruns "yt:talk01xxxxx" from "correct"
    And the worker runs until idle
    Then request 1 to "anthropic" contains "KRaft"
    And request 1 to "anthropic" contains "Confluent"
    And request 1 to "anthropic" contains "Apache Kafka"

  @COR-03 @COR-R4
  Scenario: A Traditional Chinese glossary is converted for a Simplified source
    Given a YouTube video "yt:zh001xxxxxx" with:
      | field    | value   |
      | title    | 微服务实践 |
      | language | zh-Hans |
    And the channel of "yt:zh001xxxxxx" has the overrides:
      """
      glossary: [微服務, 資料庫]
      """
    And the item "yt:zh001xxxxxx" has been processed up to "segment"
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the owner reruns "yt:zh001xxxxxx" from "correct"
    And the worker runs until idle
    Then request 1 to "anthropic" contains "微服务"
    And request 1 to "anthropic" contains "数据库"

  @COR-01 @COR-R5
  Scenario: Mixed-language speech is not translated during correction
    Given a YouTube video "yt:mix001xxxxx" titled "混合語言的分享"
    And the item "yt:mix001xxxxx" has been processed up to "segment"
    And "yt:mix001xxxxx" is a mixed-language item
    And the LLM provider "anthropic" answers "correct" with "correct-echo"
    When the owner reruns "yt:mix001xxxxx" from "correct"
    And the worker runs until idle
    Then request 1 to "anthropic" contains "保留原本的語言，不要翻譯"

  @COR-04 @COR-R6
  Scenario: New terms become pending candidates, not glossary entries
    Given the LLM provider "anthropic" answers "correct" with "correct-with-term-candidates"
    When the owner reruns "yt:talk01xxxxx" from "correct"
    And the worker runs until idle
    And the owner sends "GET /api/channels/channel-of:yt:talk01xxxxx/term-candidates"
    Then the response contains:
      """
      { "candidates": [ { "term": "KRaft", "state": "pending" } ] }
      """
    When the owner sends "GET /api/glossaries/channel/channel-of:yt:talk01xxxxx"
    Then the response contains:
      """
      { "terms": [] }
      """

  @COR-03 @COR-R7
  Scenario: The glossary is passed to ASR sources that accept hints
    Given a YouTube video "yt:nocap1xxxxx" titled "KRaft deep dive"
    And "yt:nocap1xxxxx" has no caption tracks
    And the channel of "yt:nocap1xxxxx" has the overrides:
      """
      glossary: [KRaft]
      """
    And the source "groq" returns "groq-en.json" for "yt:nocap1xxxxx"
    When the owner ingests "yt:nocap1xxxxx" with the overrides:
      """
      language: { fallback: [groq] }
      until: transcript
      """
    And the worker runs until idle
    Then the "source" artifact of "yt:nocap1xxxxx" has the provenance:
      | field         | value |
      | asr.prompt    | KRaft |

  @COR-01 @COR-R8
  Scenario: Turning correction off feeds translation directly
    Given the profile "no-correct" is:
      """
      stages:
        correct: { executor: off }
        translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
      language: { targets: [zh-Hant-TW] }
      """
    And the LLM provider "anthropic" answers "translate" with "translate-two-cues"
    When the owner sends "POST /api/items/yt:talk01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "no-correct" }
      """
    And the worker runs until idle
    Then the stage "correct" of "yt:talk01xxxxx" is "skipped"
    And the "translated" artifact of "yt:talk01xxxxx" has the provenance:
      | field            | value     |
      | parents.0        | segmented |
