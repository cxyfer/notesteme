@M2
Feature: Segment into readable sentences
  As the owner, I want transcripts re-split into readable sentences while every
  timing still comes from the source.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:talk01xxxxx" titled "Kafka talk"
    And the profile "seg-test" is:
      """
      stages:
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      timing:
        split_timing: when_needed
        split_timing_audio: auto
      """
    And the default profile is "seg-test"

  @SEG-04 @SEG-R1
  Scenario: Manual captions skip segmentation
    Given "yt:talk01xxxxx" has manual captions in "en"
    When the owner ingests "yt:talk01xxxxx" with the overrides:
      """
      until: segment
      """
    And the worker runs until idle
    Then the stage "segment" of "yt:talk01xxxxx" is "skipped"
    And the LLM provider "openai" received 0 requests

  @SEG-01 @SEG-02 @SEG-R5
  Scenario: Merging fragments keeps the outer timestamps
    Given the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text           |
      | 1 | 00:00:01 | 00:00:02 | so today we    |
      | 2 | 00:00:02 | 00:00:04 | talk about     |
      | 3 | 00:00:04 | 00:00:05 | Kafka.         |
    And the LLM provider "openai" answers "segment" with "segment-merge-all"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the "segmented" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text                         | timing |
      | 1 | 00:00:01 | 00:00:05 | so today we talk about Kafka. | merged |

  @SEG-02 @SEG-R6
  Scenario: A split inside a cue uses the word timings
    Given the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start        | end          | text                     | words                                                                                       |
      | 1 | 00:00:01.000 | 00:00:06.000 | We use Kafka. It is fast. | We@1.0-1.3 use@1.3-1.7 Kafka.@1.7-2.6 It@3.4-3.6 is@3.6-3.8 fast.@3.8-4.5                   |
    And the LLM provider "openai" answers "segment" with "segment-split-after-kafka"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the "segmented" transcript of "yt:talk01xxxxx" is:
      | # | start        | end          | text          | timing |
      | 1 | 00:00:01.000 | 00:00:02.600 | We use Kafka. | source |
      | 2 | 00:00:03.400 | 00:00:06.000 | It is fast.   | source |

  @SEG-02 @SEG-R2
  Scenario: Output that contains timestamps is rejected
    Given the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text        |
      | 1 | 00:00:01 | 00:00:04 | hello world |
    And the LLM provider "openai" answers "segment" with "segment-with-timestamps" and then "segment-merge-all"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the LLM provider "openai" received 2 requests for "segment"

  @SEG-01 @SEG-R3 @pending-m0
  Scenario: Break markers survive small punctuation changes by the model
    Given the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start        | end          | text                    | words                                                                  |
      | 1 | 00:00:01.000 | 00:00:05.000 | ok so first we install it | ok@1.0-1.2 so@1.3-1.5 first@1.6-2.0 we@3.0-3.2 install@3.2-3.8 it@3.8-4.2 |
    And the LLM provider "openai" answers "segment" with "segment-adds-commas"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the "segmented" transcript of "yt:talk01xxxxx" has 2 cues
    And the stage "segment" of "yt:talk01xxxxx" is "done"

  @SEG-01 @SEG-R3
  Scenario: A chunk whose markers cannot be aligned keeps its original split
    Given the item "yt:talk01xxxxx" has been processed up to "normalize"
    And the LLM provider "openai" answers "segment" with "segment-unalignable"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the "segmented" artifact of "yt:talk01xxxxx" has the provenance:
      | field              | value     |
      | fallbacks.0.reason | alignment |

  @SEG-01 @SEG-R4
  Scenario: Output that changes the words is rejected
    Given the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text           |
      | 1 | 00:00:01 | 00:00:04 | we use kafka   |
    And the LLM provider "openai" answers "segment" with "segment-rewrites-text" and then "segment-merge-all"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the LLM provider "openai" received 2 requests for "segment"

  @SEG-03 @SEG-R7
  Scenario: Forbid ignores splits inside a source segment
    Given the profile "strict" is:
      """
      stages:
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      timing:
        split_timing: forbid
      """
    And the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:06 | We use Kafka. It is fast. |
    And the LLM provider "openai" answers "segment" with "segment-split-after-kafka"
    When the owner sends "POST /api/items/yt:talk01xxxxx/rerun" with:
      """
      { "from": "segment", "profile": "strict" }
      """
    And the worker runs until idle
    Then the "segmented" transcript of "yt:talk01xxxxx" has 1 cue
    And every cue of the "segmented" transcript of "yt:talk01xxxxx" has the timing "source"

  @SEG-03 @SEG-R7 @SEG-R8
  Scenario: Without word timings a long cue is split at a silence
    Given the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text                                                                 |
      | 1 | 00:00:01 | 00:00:12 | this sentence is far too long to show on screen at once so we split it |
    And the audio of "yt:talk01xxxxx" is "speech-with-pause-at-6.2s.m4a"
    And the LLM provider "openai" answers "segment" with "segment-split-after-once"
    When the owner reruns "yt:talk01xxxxx" from "segment"
    And the worker runs until idle
    Then the "segmented" transcript of "yt:talk01xxxxx" is:
      | # | end          | timing  |
      | 1 | 00:00:06.200 | silence |
      | 2 | 00:00:12.000 | silence |

  @SEG-03 @SEG-R8 @SEG-R9
  Scenario: With audio downloads disabled the split is interpolated
    Given the profile "vps" is:
      """
      stages:
        segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
      timing:
        split_timing: when_needed
        split_timing_audio: never
      """
    And the "normalized" transcript of "yt:talk01xxxxx" is:
      | # | start    | end      | text                                                                 |
      | 1 | 00:00:01 | 00:00:12 | this sentence is far too long to show on screen at once so we split it |
    And the LLM provider "openai" answers "segment" with "segment-split-after-once"
    When the owner sends "POST /api/items/yt:talk01xxxxx/rerun" with:
      """
      { "from": "segment", "profile": "vps" }
      """
    And the worker runs until idle
    Then the "segmented" transcript of "yt:talk01xxxxx" is:
      | # | timing       |
      | 1 | interpolated |
      | 2 | interpolated |
    And the item "yt:talk01xxxxx" has no "audio" artifact

  @SEG-01 @SEG-R10 @pending-m0
  Scenario Outline: Subtitle rules are enforced per script
    Given the LLM provider "openai" answers "segment" with "segment-keep-all"
    And the item "<ref>" has been processed up to "segment"
    Then every cue of the "segmented" transcript of "<ref>" follows the subtitle rules for "<script>"

    Examples:
      | ref            | script |
      | yt:talk01xxxxx | latin  |
      | yt:zh001xxxxxx | cjk    |
