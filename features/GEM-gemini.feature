@M3
Feature: Notes written by Gemini from the URL
  As the owner, I want Gemini to write a note straight from a public YouTube
  URL, through the same template and checks, without downloading anything.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:gem001xxxxx" with:
      | field        | value                 |
      | title        | Paging in Ten Minutes |
      | duration     | 600                   |
      | availability | public                |
    And Gemini accepts YouTube videos up to "45m"
    And the profile "gem-test" is:
      """
      stages:
        note: { executor: api, engine: gemini, provider: gemini, model: fixture-gemini, prompt: note-gemini-default }
      """
    And the default profile is "gem-test"

  @GEM-01 @GEM-R2 @GEM-R4
  Scenario: Gemini writes the note from the URL without any download
    Given the LLM provider "gemini" answers "note" with "gemini-note-two-sections"
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    Then the stage "note" of "yt:gem001xxxxx" is "done"
    And the stage "transcript" of "yt:gem001xxxxx" is skipped with the reason "gemini"
    And the stage "frames" of "yt:gem001xxxxx" is skipped with the reason "gemini"
    And the media cache has no files of "yt:gem001xxxxx"
    And request 1 to "gemini" contains "https://www.youtube.com/watch?v=gem001xxxxx"
    And request 1 to "gemini" contains "responseSchema"
    And the note of "yt:gem001xxxxx" contains "## Page tables · [01:30](https://www.youtube.com/watch?v=gem001xxxxx&t=90s)"

  @GEM-01 @GEM-R1
  Scenario: The Gemini engine needs a Gemini provider
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "bad-gem", "config": { "stages": { "note": { "executor": "api", "engine": "gemini", "provider": "anthropic", "model": "fixture-model" } } } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "stages.note.provider", "message": "engine: gemini 需要 type 為 gemini 的 provider。" } ] }
      """

  @GEM-01 @GEM-R3 @pending-m0
  Scenario: Videos that are not public are not sent to Gemini
    Given a YouTube video "yt:gem002xxxxx" with:
      | field        | value         |
      | title        | Members draft |
      | availability | unlisted      |
    When the owner ingests "yt:gem002xxxxx"
    And the worker runs until idle
    Then the stage "note" of "yt:gem002xxxxx" is blocked with the reason "needs_action"
    And "yt:gem002xxxxx" shows the message "Gemini 只能讀取公開的 YouTube 影片，這部影片的狀態是 unlisted。"
    And "yt:gem002xxxxx" offers the actions "改用一般流程重新處理"
    And the LLM provider "gemini" received 0 requests

  @GEM-01 @GEM-R3 @pending-m0
  Scenario: Videos longer than the limit are not sent to Gemini
    Given a YouTube video "yt:gem003xxxxx" with:
      | field        | value        |
      | title        | A long class |
      | duration     | 5400         |
      | availability | public       |
    When the owner ingests "yt:gem003xxxxx"
    And the worker runs until idle
    Then the stage "note" of "yt:gem003xxxxx" is blocked with the reason "needs_action"
    And "yt:gem003xxxxx" shows the message "影片長度 1:30:00 超過 Gemini 直接生成的上限 1:00:00。"
    And the LLM provider "gemini" received 0 requests

  @GEM-01 @GEM-R3
  Scenario: Only YouTube videos can go straight to Gemini
    Given a Bilibili video "BV1gm4y1c7mD" with 1 part
    When the owner ingests "bili:BV1gm4y1c7mD/p1"
    And the worker runs until idle
    Then the stage "note" of "bili:BV1gm4y1c7mD/p1" is blocked with the reason "needs_action"
    And "bili:BV1gm4y1c7mD/p1" shows the message "Gemini 直接生成只支援 YouTube 影片。"
    And "bili:BV1gm4y1c7mD/p1" offers the actions "改用一般流程重新處理"

  @GEM-01 @GEM-R3
  Scenario: Falling back to the regular pipeline fetches the subtitles
    Given a YouTube video "yt:gem002xxxxx" with:
      | field        | value         |
      | title        | Members draft |
      | availability | unlisted      |
    And "yt:gem002xxxxx" has manual captions in "en"
    And the LLM provider "anthropic" answers "note" with "note-minimal"
    When the owner ingests "yt:gem002xxxxx"
    And the worker runs until idle
    And the owner sends "POST /api/items/yt:gem002xxxxx/rerun" with:
      """
      { "from": "transcript", "overrides": { "stages": { "note": { "engine": "pipeline", "provider": "anthropic", "model": "fixture-model", "prompt": "note-default" } } } }
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:gem002xxxxx" is "done"
    And the stage "note" of "yt:gem002xxxxx" is "done"

  @GEM-01 @GEM-R5
  Scenario: Times outside the video are retried with the error
    Given the LLM provider "gemini" answers "note" with "gemini-note-past-end" and then "gemini-note-two-sections"
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    Then the LLM provider "gemini" received 2 requests
    And request 2 to "gemini" contains "sections[1].start 12:00 超過片長 10:00"
    And the stage "note" of "yt:gem001xxxxx" is "done"

  @GEM-01 @GEM-R5
  Scenario: A Gemini note that never validates fails with a way out
    Given the LLM provider "gemini" answers "note" with "gemini-note-past-end"
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    Then the stage "note" of "yt:gem001xxxxx" failed with the error class "permanent"
    And "yt:gem001xxxxx" shows the message "Gemini 回傳的筆記沒有通過驗證（sections[1].start 12:00 超過片長 10:00）。"
    And "yt:gem001xxxxx" offers the actions "重新產生筆記, 改用一般流程重新處理"

  @GEM-01 @GEM-R6
  Scenario: Gemini notes use the same template, checks and markings
    Given the LLM provider "gemini" answers "note" with "gemini-note-two-sections"
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    Then the note of "yt:gem001xxxxx" has the frontmatter:
      | field            | value                                       |
      | source           | https://www.youtube.com/watch?v=gem001xxxxx |
      | notesteme_engine | gemini                                      |
      | notesteme_timing | model                                       |
    And the note of "yt:gem001xxxxx" contains "## 自我檢驗"
    And the "note" artifact of "yt:gem001xxxxx" has the provenance:
      | field           | value                                       |
      | engine          | gemini                                      |
      | timing          | model                                       |
      | provider        | gemini                                      |
      | gemini.file_uri | https://www.youtube.com/watch?v=gem001xxxxx |
      | checks.passed   | true                                        |

  @GEM-01 @GEM-R6
  Scenario: Untrusted markup from Gemini is neutralised too
    Given the LLM provider "gemini" answers "note" with "gemini-note-hostile"
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    Then the note of "yt:gem001xxxxx" does not contain "<script>"
    And the note of "yt:gem001xxxxx" does not contain "javascript:"

  @GEM-01 @GEM-R7 @pending-m0
  Scenario: Gemini refusing the video asks for the regular pipeline
    Given a YouTube video "yt:gem004xxxxx" with:
      | field        | value             |
      | title        | Fifty minute talk |
      | duration     | 3000              |
      | availability | public            |
    When the owner ingests "yt:gem004xxxxx"
    And the worker runs until idle
    Then the stage "note" of "yt:gem004xxxxx" is blocked with the reason "needs_action"
    And "yt:gem004xxxxx" shows the message "Gemini 無法處理這部影片（HTTP 400 INVALID_ARGUMENT），請改用一般流程。"
    And "yt:gem004xxxxx" offers the actions "改用一般流程重新處理"

  @GEM-01 @GEM-R7
  Scenario: Rate limits from Gemini are retried
    Given the LLM provider "gemini" fails with HTTP 429 once
    And the LLM provider "gemini" answers "note" with "gemini-note-two-sections"
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    And the clock advances by "1m"
    And the worker runs until idle
    Then the LLM provider "gemini" received 2 requests
    And the stage "note" of "yt:gem001xxxxx" is "done"

  @GEM-01 @GEM-R7
  Scenario: A missing Gemini key asks for one
    Given the provider "gemini" has no API key
    When the owner ingests "yt:gem001xxxxx"
    And the worker runs until idle
    Then the stage "note" of "yt:gem001xxxxx" is blocked with the reason "needs_action"
    And "yt:gem001xxxxx" offers the actions "設定 API key"
