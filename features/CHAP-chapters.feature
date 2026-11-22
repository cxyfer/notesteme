@M3
Feature: Automatic chapters
  As the owner, I want chapters for every video: platform chapters when they
  exist, otherwise chapters proposed by the model from cue numbers.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:chap02xxxxx" with:
      | field    | value               |
      | title    | Writing a Toy Kernel |
      | duration | 600                 |
    And SponsorBlock has the segments for "yt:chap02xxxxx":
      | category | start    | end      |
      | sponsor  | 00:03:00 | 00:03:30 |
    And the profile "chap-test" is:
      """
      stages:
        chapters: { executor: api, provider: anthropic, model: fixture-model, prompt: chapters-default }
      """
    And the default profile is "chap-test"
    And the "corrected" transcript of "yt:chap02xxxxx" is:
      | # | start    | end      | text                                   |
      | 1 | 00:00:05 | 00:00:10 | Welcome back to the channel.           |
      | 2 | 00:00:20 | 00:00:25 | Today we set up the toolchain.         |
      | 3 | 00:03:05 | 00:03:10 | This video is sponsored by NordWidget. |
      | 4 | 00:05:00 | 00:05:05 | Now the boot loader.                   |
      | 5 | 00:09:00 | 00:09:05 | It jumps into the kernel.              |

  @CHAP-01 @CHAP-R1
  Scenario: Platform chapters are used as they are
    Given a YouTube video "yt:chap03xxxxx" with:
      | field    | value                              |
      | title    | A video with chapters              |
      | duration | 900                                |
      | chapters | 00:00 Intro; 05:00 Main; 12:00 Q&A |
    And "yt:chap03xxxxx" has manual captions in "en"
    When the owner ingests "yt:chap03xxxxx"
    And the worker runs until idle
    Then the stage "chapters" of "yt:chap03xxxxx" is skipped with the reason "platform_chapters"
    And the LLM provider "anthropic" received 0 requests for "chapters"
    When the owner sends "GET /api/items/yt:chap03xxxxx"
    Then the response contains:
      """
      { "chapters": [
          { "title": "Intro", "start": "00:00:00.000", "end": "00:05:00.000", "origin": "platform" },
          { "title": "Main",  "start": "00:05:00.000", "end": "00:12:00.000", "origin": "platform" },
          { "title": "Q&A",   "start": "00:12:00.000", "end": "00:15:00.000", "origin": "platform" }
      ] }
      """

  @CHAP-01 @CHAP-R2 @CHAP-R5
  Scenario: The model proposes chapters by cue number and the times come from the cues
    Given the LLM provider "anthropic" answers "chapters" with "chapters-two"
    When the owner reruns "yt:chap02xxxxx" from "chapters"
    And the worker runs until idle
    Then the stage "chapters" of "yt:chap02xxxxx" is "done"
    And request 1 to "anthropic" contains "Today we set up the toolchain."
    And request 1 to "anthropic" does not contain "00:05:00"
    When the owner sends "GET /api/items/yt:chap02xxxxx"
    Then the response contains:
      """
      { "chapters": [
          { "title": "Setting up the toolchain", "start": "00:00:00.000", "end": "00:05:00.000", "origin": "llm" },
          { "title": "The boot loader",          "start": "00:05:00.000", "end": "00:10:00.000", "origin": "llm" }
      ] }
      """

  @CHAP-01 @CHAP-R3
  Scenario Outline: Invalid chapters are retried with the error
    Given the LLM provider "anthropic" answers "chapters" with "<fixture>" and then "chapters-two"
    When the owner reruns "yt:chap02xxxxx" from "chapters"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 2 requests for "chapters"
    And request 2 to "anthropic" contains "<error>"
    And the stage "chapters" of "yt:chap02xxxxx" is "done"

    Examples:
      | fixture                 | error                       |
      | chapters-missing-cue    | chapters[1].cue 99 不在輸入中 |
      | chapters-out-of-order   | chapters[1].cue 必須遞增      |
      | chapters-title-too-long | chapters[0].title 超過 80 字  |

  @CHAP-01 @CHAP-R4
  Scenario: Chapters that never validate leave the item without chapters
    Given the LLM provider "anthropic" answers "chapters" with "chapters-missing-cue"
    When the owner reruns "yt:chap02xxxxx" from "chapters"
    And the worker runs until idle
    Then the stage "chapters" of "yt:chap02xxxxx" is "done"
    And "yt:chap02xxxxx" shows the message "無法產生章節：模型回傳的內容沒有通過驗證（chapters[1].cue 99 不在輸入中）。筆記不會分章節。"
    And "yt:chap02xxxxx" offers the actions "重新產生章節, 開啟 Prompt"
    When the owner sends "GET /api/items/yt:chap02xxxxx"
    Then the response contains:
      """
      { "chapters": [] }
      """

  @CHAP-01 @CHAP-R6
  Scenario: Cues inside a sponsor segment are not offered as chapter starts
    Given the LLM provider "anthropic" answers "chapters" with "chapters-in-sponsor" and then "chapters-two"
    When the owner reruns "yt:chap02xxxxx" from "chapters"
    And the worker runs until idle
    Then request 1 to "anthropic" does not contain "NordWidget"
    And request 2 to "anthropic" contains "chapters[1].cue 3 不在輸入中"

  @CHAP-01 @CHAP-R7
  Scenario: Generated chapters are stored with their provenance
    Given the LLM provider "anthropic" answers "chapters" with "chapters-two"
    When the owner reruns "yt:chap02xxxxx" from "chapters"
    And the worker runs until idle
    Then the "chapters" artifact of "yt:chap02xxxxx" has the provenance:
      | field     | value                       |
      | stage     | chapters                    |
      | origin    | llm                         |
      | provider  | anthropic                   |
      | parents   | corrected,sponsor_segments  |

  @mcp @CHAP-01 @CHAP-R7
  Scenario: Agents get the chapters with the item
    Given a token "reader" with scopes "read"
    And the LLM provider "anthropic" answers "chapters" with "chapters-two"
    When the owner reruns "yt:chap02xxxxx" from "chapters"
    And the worker runs until idle
    And the MCP client with the token "reader" calls "get_item" with:
      """
      { "item": "yt:chap02xxxxx" }
      """
    Then the tool result contains:
      """
      { "chapters": [ { "title": "Setting up the toolchain", "start": "00:00:00.000" }, { "title": "The boot loader" } ] }
      """

  @CHAP-01 @CHAP-R8
  Scenario: Chapters can be turned off
    When the owner sends "POST /api/items/yt:chap02xxxxx/rerun" with:
      """
      { "from": "chapters", "overrides": { "stages": { "chapters": { "executor": "off" } } } }
      """
    And the worker runs until idle
    Then the stage "chapters" of "yt:chap02xxxxx" is skipped with the reason "off"
    And the LLM provider "anthropic" received 0 requests for "chapters"

  @CHAP-01 @CHAP-R8
  Scenario: Chapters can be handed to an agent
    When the owner sends "POST /api/items/yt:chap02xxxxx/rerun" with:
      """
      { "from": "chapters", "overrides": { "stages": { "chapters": { "executor": "agent" } } } }
      """
    And the worker runs until idle
    Then the stage "chapters" of "yt:chap02xxxxx" is "awaiting_agent"
