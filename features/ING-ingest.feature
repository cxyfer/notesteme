@M1
Feature: Ingest media
  As the owner, I want to paste video URLs, pick parts or episodes, or drop local
  files, and have Notesteme process them without creating duplicates.

  Background:
    Given the owner is signed in

  @ING-02 @ING-R1
  Scenario Outline: Recognise what a URL points to
    When the owner resolves "<url>"
    Then the response status is 200
    And the response contains:
      """
      { "platform": "<platform>", "kind": "<kind>" }
      """

    Examples:
      | url                                                   | platform | kind     |
      | https://www.youtube.com/watch?v=abc123                | youtube  | video    |
      | https://youtu.be/abc123                               | youtube  | video    |
      | https://www.youtube.com/shorts/abc123                 | youtube  | video    |
      | https://www.youtube.com/playlist?list=PL123           | youtube  | playlist |
      | https://www.youtube.com/@somechannel                  | youtube  | channel  |
      | https://www.bilibili.com/video/BV1xx411c7mD?p=2       | bilibili | video    |
      | https://space.bilibili.com/12345                      | bilibili | channel  |
      | https://feeds.example.com/show.xml                    | podcast  | feed     |

  @ING-02 @ING-R1
  Scenario: Reject a URL that no platform recognises
    When the owner resolves "https://example.com/some/page"
    Then the response status is 422
    And the response contains:
      """
      { "message": "無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。" }
      """

  @ING-01 @ING-R12
  Scenario: Ingesting a YouTube video creates an item and starts the pipeline
    Given a YouTube video "yt:abc123" titled "PostgreSQL in 100 Seconds"
    When the owner ingests "https://www.youtube.com/watch?v=abc123"
    Then the response status is 201
    And the library contains "yt:abc123"
    And the stage "metadata" of "yt:abc123" is "pending"

  @ING-06 @ING-R2
  Scenario: Ingesting an item that is already in the library does not duplicate it
    Given a YouTube video "yt:abc123" titled "PostgreSQL in 100 Seconds"
    And the item "yt:abc123" has been ingested
    When the owner ingests "https://youtu.be/abc123"
    Then the response status is 200
    And the response contains:
      """
      { "items": [ { "ref": "yt:abc123", "already_in_library": true } ] }
      """
    And the library contains 1 item

  @ING-03 @ING-R3
  Scenario: A multi-part Bilibili video becomes one item per part
    Given a Bilibili video "BV1xx411c7mD" with 3 parts
    When the owner ingests "https://www.bilibili.com/video/BV1xx411c7mD"
    Then the library contains 3 items
    And the library contains "bili:BV1xx411c7mD/p3"

  @ING-03 @ING-R3
  Scenario: Only the selected Bilibili parts are ingested
    Given a Bilibili video "BV1xx411c7mD" with 3 parts
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://www.bilibili.com/video/BV1xx411c7mD", "parts": [1, 3] }
      """
    Then the library contains 2 items
    And the library contains "bili:BV1xx411c7mD/p1"
    And the library contains "bili:BV1xx411c7mD/p3"

  @ING-01 @ING-R4
  Scenario: A playlist is expanded once into its videos
    Given the URL "https://www.youtube.com/playlist?list=PL123" serves "playlist-3-videos.json"
    When the owner ingests "https://www.youtube.com/playlist?list=PL123"
    Then the library contains 3 items

  @ING-01 @ING-R5
  Scenario: A channel cannot be ingested directly
    When the owner ingests "https://www.youtube.com/@somechannel"
    Then the response status is 422
    And the response contains:
      """
      { "message": "這是頻道。要追蹤新內容，請建立訂閱。" }
      """
    And the library contains 0 items

  @ING-04 @ING-R6
  Scenario: Pick a single episode from a podcast feed
    Given a podcast feed "show" with the episodes:
      | guid  | title     | audio       |
      | ep-41 | Episode 41 | ep41.mp3   |
      | ep-42 | Episode 42 | ep42.mp3   |
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://feeds.example.com/show.xml", "episodes": ["ep-42"] }
      """
    Then the library contains 1 item
    And the library contains "pod:show/ep-42"

  @ING-04 @ING-R6
  Scenario: A podcast feed without selected episodes is refused
    Given a podcast feed "show" with the episodes:
      | guid  | title      | audio    |
      | ep-42 | Episode 42 | ep42.mp3 |
    When the owner ingests "https://feeds.example.com/show.xml"
    Then the response status is 422

  @ING-05 @ING-R10
  Scenario: A file dropped into the inbox is ingested once it stops growing
    Given a local file "talk.mp4" in the inbox
    When the clock advances by "5s"
    And the worker runs until idle
    Then the library contains 0 items
    When the clock advances by "10s"
    And the worker runs until idle
    Then the library contains "local:talk.mp4"

  @ING-05 @ING-R10
  Scenario: The same file content is not ingested twice
    Given a local file "talk.mp4" in the inbox
    And a local file "talk-copy.mp4" in the inbox
    When the clock advances by "15s"
    And the worker runs until idle
    Then the library contains 1 item

  @ING-05 @ING-R11
  Scenario: A file without audio or video is left alone
    Given a local file "notes.pdf" in the inbox
    When the clock advances by "15s"
    And the worker runs until idle
    Then the library contains 0 items
    And the file "/data/inbox/notes.pdf" exists

  @ING-05 @ING-R11
  Scenario: Upload a local file through the API
    When the owner uploads "lecture.m4a"
    Then the response status is 201
    And the library contains "local:lecture.m4a"

  @ING-07 @ING-R7
  Scenario: Per-run overrides do not change the profile
    Given a YouTube video "yt:abc123" titled "PostgreSQL in 100 Seconds"
    And the profile "standard" is:
      """
      language:
        targets: [zh-Hant-TW]
      """
    When the owner ingests "yt:abc123" with the overrides:
      """
      language:
        targets: [en]
      """
    Then the effective config of "yt:abc123" for "transcript" contains:
      """
      language:
        targets: [en]
      """
    When the owner sends "GET /api/profiles/standard"
    Then the response contains:
      """
      { "config": { "language": { "targets": ["zh-Hant-TW"] } } }
      """

  @ING-08 @ING-R8
  Scenario: The estimate leaves out ASR when platform captions exist
    Given a YouTube video "yt:abc123" titled "PostgreSQL in 100 Seconds"
    And "yt:abc123" has auto captions in "en"
    When the owner sends "POST /api/estimate" with:
      """
      { "url": "https://www.youtube.com/watch?v=abc123" }
      """
    Then the response status is 200
    And the response contains:
      """
      { "items": [ { "expected_source": "youtube-auto", "asr_usd": 0 } ] }
      """
    And the library contains 0 items

  @ING-08 @ING-R9
  Scenario: Content that needs a cookie is flagged before submitting
    Given a YouTube video "yt:members1" with:
      | field        | value           |
      | title        | Members only    |
      | availability | subscriber_only |
    When the owner resolves "https://www.youtube.com/watch?v=members1"
    Then the response contains:
      """
      { "requires_cookie": true, "settings_link": "/settings/accounts" }
      """
