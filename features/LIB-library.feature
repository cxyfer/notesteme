Feature: Library, item page and queue page
  As the owner, I want to browse and filter my library, read subtitles next to
  the player, and see the queue, with everything updating live.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:pg100" titled "PostgreSQL in 100 Seconds"
    And a YouTube video "yt:agile43" titled "我们都只是在假装着做Agile"
    And a Bilibili video "BV1xx411c7mD" with 1 part
    And the item "yt:pg100" has been processed up to "normalize"
    And the item "yt:agile43" has been ingested
    And the item "bili:BV1xx411c7mD/p1" has been ingested

  @M1 @LIB-01 @LIB-R1
  Scenario: Filter by platform
    When the owner sends "GET /api/items?platform=bilibili"
    Then the response contains:
      """
      { "items": [ { "ref": "bili:BV1xx411c7mD/p1" } ], "total": 1 }
      """

  @M1 @LIB-01 @LIB-R1
  Scenario: Filter by stage status
    When the owner sends "GET /api/items?status=normalize:done"
    Then the response contains:
      """
      { "items": [ { "ref": "yt:pg100" } ], "total": 1 }
      """

  @M1 @LIB-01 @LIB-R1
  Scenario: Conditions are combined with AND
    When the owner sends "GET /api/items?platform=youtube&status=normalize:done"
    Then the response contains:
      """
      { "total": 1 }
      """

  @M1 @LIB-02 @LIB-R3
  Scenario Outline: Title search matches partial words in any language
    When the owner sends "GET /api/items?q=<query>"
    Then the response contains:
      """
      { "items": [ { "ref": "<ref>" } ], "total": 1 }
      """

    Examples:
      | query      | ref        |
      | postgresql | yt:pg100   |
      | Agile      | yt:agile43 |
      | 假装       | yt:agile43 |

  @M1 @LIB-01 @LIB-R4
  Scenario: Pages are limited and use a cursor
    When the owner sends "GET /api/items?limit=2"
    Then the response contains:
      """
      { "total": 3, "next_cursor": "*" }
      """

  @M1 @LIB-03 @LIB-R7
  Scenario: Items with estimated timings can be filtered
    Given the "normalized" transcript of "yt:agile43" is:
      | # | start    | end      | text  | timing       |
      | 1 | 00:00:01 | 00:00:03 | 第一句 | source       |
      | 2 | 00:00:03 | 00:00:05 | 第二句 | interpolated |
    When the owner sends "GET /api/items?timing=interpolated"
    Then the response contains:
      """
      { "items": [ { "ref": "yt:agile43" } ], "total": 1 }
      """

  @M1 @LIB-03 @LIB-R6
  Scenario: Local media supports range requests for seeking
    Given a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been ingested
    When the owner sends "GET /api/items/local:lecture.mp4/media" with the header "Range" set to "bytes=0-1023"
    Then the response status is 206

  @M1 @LIB-04 @LIB-R8
  Scenario: The queue shows each lane with its limit
    Given the concurrency limit of "download" is 2
    When the owner sends "GET /api/queue"
    Then the response contains:
      """
      { "lanes": [ { "class": "download", "limit": 2, "running": 0, "waiting": 2 } ] }
      """

  @M1 @LIB-02 @LIB-R2 @ui @manual
  Scenario: Filters survive a reload
    Given the owner has filtered the library by platform "YouTube"
    When the owner reloads the page
    Then the library still shows only YouTube items
    And the address bar contains "platform=youtube"

  @M1 @LIB-03 @LIB-R5 @LIB-R6 @ui @manual
  Scenario: Clicking a line seeks the player
    Given the owner is on the item page of "yt:pg100"
    When the owner clicks the line at 00:01:05
    Then the YouTube player jumps to 00:01:05
    And switching between "原始" and "正規化" keeps the scroll position

  @M1 @LIB-05 @LIB-R9 @ui @manual
  Scenario: New items do not reshuffle the list
    Given the owner is scrolled halfway down the library
    When three new items are ingested
    Then a notice "有 3 個新項目" appears
    And the visible rows stay where they are

  @M1 @LIB-01 @LIB-R10 @ui @manual
  Scenario: An empty filter result offers to clear the filters
    Given the owner filters the library by a channel with no items
    Then the page shows "沒有符合條件的影音。"
    And a "清除篩選條件" button is shown

  @M2 @LIB-06 @LIB-R12 @ui @manual
  Scenario: The diff view highlights what the LLM changed
    Given "yt:agile43" has corrected subtitles
    When the owner opens the "差異" mode
    Then each changed character is highlighted against the original

  @M2 @LIB-07 @LIB-R11
  Scenario: Rerun from a stage with another model keeps the old version
    Given the LLM provider "anthropic" answers "correct" with "correct-ok.json"
    And the LLM provider "openai" answers "correct" with "correct-ok-alt.json"
    And the item "yt:pg100" has been processed up to "correct"
    When the owner sends "POST /api/items/yt:pg100/rerun" with:
      """
      { "from": "correct", "overrides": { "stages": { "correct": { "provider": "openai", "model": "fixture-model" } } } }
      """
    And the worker runs until idle
    Then the "corrected" artifact of "yt:pg100" has 2 versions
