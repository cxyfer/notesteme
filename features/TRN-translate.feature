@M2
Feature: Translate and convert Chinese scripts
  As the owner, I want subtitles translated into my target languages with long
  sentences kept whole, bad output handled safely, and Chinese script
  differences converted or localised as I choose.

  Background:
    Given the owner is signed in
    And the profile "trn-test" is:
      """
      stages:
        translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
      language:
        targets: [zh-Hant-TW]
        variant_strategy: convert
      """
    And the default profile is "trn-test"
    And a YouTube video "yt:talk01xxxxx" with:
      | field    | value      |
      | title    | Kafka talk |
      | language | en         |

  @TRN-01 @TRN-R1
  Scenario: Several targets share one corrected transcript
    Given the item "yt:talk01xxxxx" has been processed up to "correct"
    And the LLM provider "anthropic" answers "translate" with "translate-echo-target"
    When the owner sends "POST /api/items/yt:talk01xxxxx/rerun" with:
      """
      { "from": "translate", "overrides": { "language": { "targets": ["zh-Hant-TW", "ja"] } } }
      """
    And the worker runs until idle
    Then the stage "translate" of "yt:talk01xxxxx" is "done"
    And the "translated" transcript of "yt:talk01xxxxx" in "zh-Hant-TW" has 120 cues
    And the "translated" transcript of "yt:talk01xxxxx" in "ja" has 120 cues
    And the "corrected" artifact of "yt:talk01xxxxx" has 1 version

  @TRN-02 @TRN-R2 @TRN-R3
  Scenario: A sentence spanning two cues is translated as one and split back
    Given the "corrected" transcript of "yt:talk01xxxxx" in "en" is:
      | # | start    | end      | text                                   |
      | 1 | 00:00:01 | 00:00:03 | The reason we moved to Kafka was that  |
      | 2 | 00:00:03 | 00:00:05 | our old queue could not keep up.       |
      | 3 | 00:00:06 | 00:00:08 | It worked.                             |
    And the LLM provider "anthropic" answers "translate" with "translate-group-with-marker"
    When the owner reruns "yt:talk01xxxxx" from "translate"
    And the worker runs until idle
    Then the "translated" transcript of "yt:talk01xxxxx" in "zh-Hant-TW" is:
      | # | start    | end      | text                       |
      | 1 | 00:00:01 | 00:00:03 | 我們之所以改用 Kafka，     |
      | 2 | 00:00:03 | 00:00:05 | 是因為舊的佇列跟不上。     |
      | 3 | 00:00:06 | 00:00:08 | 結果很成功。               |
    And request 1 to "anthropic" contains "⟨|⟩"

  @TRN-02 @TRN-R4 @pending-m0
  Scenario: A wrong number of split markers falls back to cue-by-cue translation
    Given the "corrected" transcript of "yt:talk01xxxxx" in "en" is:
      | # | start    | end      | text                                  |
      | 1 | 00:00:01 | 00:00:03 | The reason we moved to Kafka was that |
      | 2 | 00:00:03 | 00:00:05 | our old queue could not keep up.      |
    And the LLM provider "anthropic" answers "translate" with "translate-group-missing-marker" and then "translate-per-cue"
    When the owner reruns "yt:talk01xxxxx" from "translate"
    And the worker runs until idle
    Then the "translated" transcript of "yt:talk01xxxxx" in "zh-Hant-TW" has 2 cues
    And the LLM provider "anthropic" received 2 requests for "translate"

  @TRN-03 @TRN-R5
  Scenario Outline: Unacceptable translations are retried
    Given the "corrected" transcript of "yt:talk01xxxxx" in "en" is:
      | # | start    | end      | text                         |
      | 1 | 00:00:01 | 00:00:04 | We store events in a log.    |
    And the LLM provider "anthropic" answers "translate" with "<bad>" and then "translate-store-events"
    When the owner reruns "yt:talk01xxxxx" from "translate"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 2 requests for "translate"
    And request 2 to "anthropic" contains "<error>"

    Examples:
      | bad                        | error           |
      | translate-in-japanese      | wrong_language  |
      | translate-empty            | empty           |
      | translate-untranslated     | untranslated    |
      | translate-way-too-long     | length_ratio    |

  @TRN-03 @TRN-R5
  Scenario: A proper noun may stay as it is
    Given the "corrected" transcript of "yt:talk01xxxxx" in "en" is:
      | # | start    | end      | text   |
      | 1 | 00:00:01 | 00:00:02 | Kafka. |
    And the LLM provider "anthropic" answers "translate" with "translate-kafka-unchanged"
    When the owner reruns "yt:talk01xxxxx" from "translate"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 1 request for "translate"

  @TRN-03 @TRN-R6
  Scenario: A translation that reads too fast is kept with a warning
    Given the "corrected" transcript of "yt:talk01xxxxx" in "en" is:
      | # | start        | end          | text            |
      | 1 | 00:00:01.000 | 00:00:01.900 | Okay, let's go. |
    And the LLM provider "anthropic" answers "translate" with "translate-long-for-short-cue"
    When the owner reruns "yt:talk01xxxxx" from "translate"
    And the worker runs until idle
    Then the stage "translate" of "yt:talk01xxxxx" is "done"
    And the "translated" artifact of "yt:talk01xxxxx" has the provenance:
      | field             | value         |
      | warnings.0.cue    | 1             |
      | warnings.0.reason | reading_speed |

  @TRN-03 @TRN-R7
  Scenario: A cue that never validates keeps the original text
    Given the "corrected" transcript of "yt:talk01xxxxx" in "en" is:
      | # | start    | end      | text                      |
      | 1 | 00:00:01 | 00:00:04 | We store events in a log. |
    And the LLM provider "anthropic" answers "translate" with "translate-empty"
    When the owner reruns "yt:talk01xxxxx" from "translate"
    And the worker runs until idle
    Then the "translated" transcript of "yt:talk01xxxxx" in "zh-Hant-TW" is:
      | # | text                      |
      | 1 | We store events in a log. |
    And the "translated" artifact of "yt:talk01xxxxx" has the provenance:
      | field              | value |
      | fallbacks.0.reason | empty |

  @TRN-04 @TRN-R8
  Scenario Outline: Chinese script conversion uses OpenCC without the LLM
    Given a YouTube video "yt:zh001xxxxxx" with:
      | field    | value       |
      | title    | 微服务实践  |
      | language | <source>    |
    And the "corrected" transcript of "yt:zh001xxxxxx" in "<source>" is:
      | # | start    | end      | text     |
      | 1 | 00:00:01 | 00:00:03 | <before> |
    When the owner sends "POST /api/items/yt:zh001xxxxxx/rerun" with:
      """
      { "from": "translate", "overrides": { "language": { "targets": ["<target>"], "variant_strategy": "convert" } } }
      """
    And the worker runs until idle
    Then the "translated" transcript of "yt:zh001xxxxxx" in "<target>" is:
      | # | text    |
      | 1 | <after> |
    And the "translated" artifact of "yt:zh001xxxxxx" has the provenance:
      | field    | value  |
      | executor | opencc |
      | table    | <table> |
    And the LLM provider "anthropic" received 0 requests

    Examples:
      | source  | target     | table | before         | after          |
      | zh-Hans | zh-Hant-TW | s2twp | 打开软件的设置 | 開啟軟體的設定 |
      | zh-Hans | zh-Hant-HK | s2hk  | 打开软件的设置 | 打開軟件的設置 |
      | zh-Hant | zh-Hans    | t2s   | 這是一個測試   | 这是一个测试   |

  @TRN-04 @TRN-R9
  Scenario: Localisation runs OpenCC first and lets the LLM fix wording
    Given a YouTube video "yt:zh001xxxxxx" with:
      | field    | value      |
      | title    | 微服务实践 |
      | language | zh-Hans    |
    And the "corrected" transcript of "yt:zh001xxxxxx" in "zh-Hans" is:
      | # | start    | end      | text               |
      | 1 | 00:00:01 | 00:00:04 | 这个视频的质量很好 |
    And the LLM provider "anthropic" answers "translate" with "localize-video-quality"
    When the owner sends "POST /api/items/yt:zh001xxxxxx/rerun" with:
      """
      { "from": "translate", "overrides": { "language": { "targets": ["zh-Hant-TW"], "variant_strategy": "localize" } } }
      """
    And the worker runs until idle
    Then request 1 to "anthropic" contains "這個視頻的質量很好"
    And the "translated" transcript of "yt:zh001xxxxxx" in "zh-Hant-TW" is:
      | # | text               |
      | 1 | 這部影片的品質很好 |
    And the "translated" artifact of "yt:zh001xxxxxx" has the provenance:
      | field     | value               |
      | prompt.id | localize-zh-hant-tw |

  @TRN-01 @TRN-R10
  Scenario: Same source and target language skips translation
    Given a YouTube video "yt:tw001xxxxxx" with:
      | field    | value      |
      | title    | 台灣的影片 |
      | language | zh-Hant-TW |
    And the item "yt:tw001xxxxxx" has been processed up to "correct"
    When the owner reruns "yt:tw001xxxxxx" from "translate"
    And the worker runs until idle
    Then the stage "translate" of "yt:tw001xxxxxx" is "skipped"

  @TRN-05 @TRN-R11
  Scenario: A manual subtitle track in the target language is reused
    Given "yt:talk01xxxxx" has manual captions in "en"
    And "yt:talk01xxxxx" has manual captions in "zh-Hant-TW"
    When the owner ingests "yt:talk01xxxxx"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 0 requests for "translate"
    And the "translated" artifact of "yt:talk01xxxxx" has the provenance:
      | field    | value          |
      | executor | platform       |
      | source   | youtube-manual |
