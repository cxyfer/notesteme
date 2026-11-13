Feature: Subtitle output and import
  As the owner, I want subtitles rendered on demand in any format, written where
  I need them without overwriting my edits, and I want to import subtitles I
  fixed elsewhere.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:abc123xxxxx" titled "Kafka: in 100 seconds?"
    And the "corrected" transcript of "yt:abc123xxxxx" in "en" is:
      | # | start        | end          | text                   |
      | 1 | 00:00:01.000 | 00:00:03.500 | We use Kafka for this. |
      | 2 | 00:00:04.000 | 00:00:06.000 | It is a log.           |

  @M1 @OUT-01 @OUT-R1
  Scenario: Download SRT rendered from the cues
    When the owner sends "GET /api/items/yt:abc123xxxxx/subtitles?format=srt"
    Then the response status is 200
    And the response header "Content-Type" contains "application/x-subrip"
    And the item "yt:abc123xxxxx" has no "srt" artifact

  @M1 @OUT-01 @OUT-R1
  Scenario Outline: Every M1 format is available
    When the owner sends "GET /api/items/yt:abc123xxxxx/subtitles?format=<format>"
    Then the response status is 200
    And the response header "Content-Type" contains "<type>"

    Examples:
      | format    | type                    |
      | srt       | application/x-subrip    |
      | vtt       | text/vtt                |
      | txt       | text/plain              |
      | cues-json | application/json        |

  @M1 @OUT-01 @OUT-R2
  Scenario: The most processed variant is used by default
    Given the "normalized" transcript of "yt:abc123xxxxx" in "en" is:
      | # | start        | end          | text                  |
      | 1 | 00:00:01.000 | 00:00:03.500 | we use cafca for this |
    When the owner sends "GET /api/items/yt:abc123xxxxx/cues"
    Then the response contains:
      """
      { "variant": "corrected", "cues": [ { "text": "We use Kafka for this." } ] }
      """

  @M1 @OUT-01 @OUT-R3
  Scenario: A time range keeps the original timestamps
    When the owner sends "GET /api/items/yt:abc123xxxxx/cues?from=00:00:03.800&to=00:00:10"
    Then the response contains:
      """
      { "cues": [ { "start": "00:00:04.000", "text": "It is a log." } ] }
      """

  @M1 @OUT-01 @OUT-R4
  Scenario: The download file name is safe on Windows
    When the owner sends "GET /api/items/yt:abc123xxxxx/subtitles?format=srt"
    Then the response header "Content-Disposition" contains "Kafka in 100 seconds.en.srt"

  @M2 @OUT-02 @OUT-R5
  Scenario: Bilingual ASS puts both languages in each cue
    Given the "translated" transcript of "yt:abc123xxxxx" in "zh-Hant-TW" is:
      | # | start        | end          | text                 |
      | 1 | 00:00:01.000 | 00:00:03.500 | 我們在這裡用 Kafka。 |
      | 2 | 00:00:04.000 | 00:00:06.000 | 它是一個日誌。       |
    When the owner sends "GET /api/items/yt:abc123xxxxx/subtitles?format=ass&lang=zh-Hant-TW&style=default"
    Then the response status is 200
    And the response header "Content-Type" contains "text/x-ssa"

  @M2 @OUT-02 @OUT-R5
  Scenario: Same-language content is never rendered as bilingual
    Given a YouTube video "yt:zh001xxxxxx" titled "简中影片"
    And the "translated" transcript of "yt:zh001xxxxxx" in "zh-Hant-TW" is:
      | # | start        | end          | text       |
      | 1 | 00:00:01.000 | 00:00:03.000 | 這是一部影片 |
    And the "corrected" transcript of "yt:zh001xxxxxx" in "zh-Hans" is:
      | # | start        | end          | text       |
      | 1 | 00:00:01.000 | 00:00:03.000 | 这是一部视频 |
    When the owner sends "GET /api/items/yt:zh001xxxxxx/subtitles?format=bilingual-srt&lang=zh-Hant-TW"
    Then the response status is 200
    And the response contains:
      """
      { "bilingual": false }
      """

  @M2 @OUT-03 @OUT-R6
  Scenario: Built-in styles cannot be modified
    When the owner sends "PATCH /api/ass-styles/default" with:
      """
      { "primary": { "size": 60 } }
      """
    Then the response status is 409

  @M2 @OUT-03 @OUT-R7
  Scenario Outline: The default font follows the target script
    When the owner sends "GET /api/ass-styles/default?lang=<lang>"
    Then the response contains:
      """
      { "translation": { "font": "<font>" } }
      """

    Examples:
      | lang       | font             |
      | zh-Hant-TW | Noto Sans CJK TC |
      | zh-Hans-CN | Noto Sans CJK SC |
      | en         | Noto Sans        |

  @M2 @OUT-03 @OUT-R8
  Scenario: Import styles from an existing ASS file
    Given the file "/tmp/fansub.ass" contains:
      """
      [V4+ Styles]
      Format: Name, Fontname, Fontsize, PrimaryColour, OutlineColour, Outline
      Style: Default,Source Han Sans TC,54,&H00FFFFFF,&H00000000,2
      """
    When the owner sends "POST /api/ass-styles/import" with:
      """
      { "path": "/tmp/fansub.ass", "name": "fansub" }
      """
    Then the response status is 201
    And the response contains:
      """
      { "name": "fansub", "builtin": false }
      """

  @M2 @OUT-03 @OUT-R9
  Scenario: Preview a style on a real frame
    Given the item "yt:abc123xxxxx" has been processed up to "transcript"
    When the owner sends "POST /api/ass-styles/preview" with:
      """
      { "style": "default", "item": "yt:abc123xxxxx", "at": "00:00:02" }
      """
    Then the response status is 200
    And the response header "Content-Type" contains "image/png"

  @M2 @OUT-04 @OUT-R10
  Scenario: Subtitles are written next to a local video
    Given a local file "lecture.mp4" in the inbox
    And the profile "media-server" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en] }
      """
    And the item "local:lecture.mp4" has been processed up to "correct"
    When the owner reruns "local:lecture.mp4" from "publish"
    And the worker runs until idle
    Then the file "/data/inbox/lecture.en.srt" exists

  @M2 @OUT-04 @OUT-R10
  Scenario: A sidecar target is skipped for online videos
    Given the profile "media-server" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en] }
      """
    When the owner ingests "yt:abc123xxxxx" with the profile "media-server"
    And the worker runs until idle
    Then the stage "publish" of "yt:abc123xxxxx" is "skipped"

  @M2 @OUT-05 @OUT-R11 @OUT-R12
  Scenario: A file edited by hand is saved alongside instead of overwritten
    Given the clock is at "2026-11-12T10:00:00Z"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "publish"
    And the file "/data/inbox/lecture.en.srt" contains:
      """
      1
      00:00:01,000 --> 00:00:03,500
      I fixed this line by hand.
      """
    When the owner reruns "local:lecture.mp4" from "publish"
    And the worker runs until idle
    Then the file "/data/inbox/lecture.en.srt" contains "I fixed this line by hand."
    And the file "/data/inbox/lecture.en (notesteme 2026-11-12).srt" exists

  @M2 @OUT-05 @OUT-R12
  Scenario: With the skip policy an edited file blocks publishing
    Given the profile "careful" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en], on_user_change: skip }
      """
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "publish"
    And the file "/data/inbox/lecture.en.srt" contains:
      """
      1
      00:00:01,000 --> 00:00:03,500
      I fixed this line by hand.
      """
    When the owner sends "POST /api/items/local:lecture.mp4/rerun" with:
      """
      { "from": "publish", "profile": "careful" }
      """
    And the worker runs until idle
    Then the stage "publish" of "local:lecture.mp4" is blocked with the reason "needs_action"

  @M2 @OUT-06 @OUT-R13
  Scenario: Import a subtitle fixed in an external editor
    Given the file "/tmp/fixed.srt" contains:
      """
      1
      00:00:01,000 --> 00:00:03,500
      We use Apache Kafka for this.
      """
    When the owner sends "POST /api/items/yt:abc123xxxxx/subtitles/import" with:
      """
      { "path": "/tmp/fixed.srt", "lang": "en" }
      """
    Then the response status is 201
    And the "edited" artifact of "yt:abc123xxxxx" has the provenance:
      | field    | value |
      | executor | human |
    And the stage "translate" of "yt:abc123xxxxx" is "pending"

  @M2 @OUT-06 @OUT-R14
  Scenario: Existing translations are flagged after an import
    Given the "translated" transcript of "yt:abc123xxxxx" in "zh-Hant-TW" is:
      | # | start        | end          | text                 |
      | 1 | 00:00:01.000 | 00:00:03.500 | 我們在這裡用 Kafka。 |
    And the file "/tmp/fixed.srt" contains:
      """
      1
      00:00:01,000 --> 00:00:03,500
      We use Apache Kafka for this.
      """
    When the owner sends "POST /api/items/yt:abc123xxxxx/subtitles/import" with:
      """
      { "path": "/tmp/fixed.srt", "lang": "en" }
      """
    Then "yt:abc123xxxxx" shows the message "字幕已更新，譯文可能過時"
    And "yt:abc123xxxxx" offers the actions "重新翻譯"

  @M2 @OUT-06 @OUT-R15
  Scenario: An import with broken timings points at the bad line
    Given the file "/tmp/broken.srt" contains:
      """
      1
      00:00:05,000 --> 00:00:06,000
      first

      2
      00:00:02,000 --> 00:00:01,000
      second
      """
    When the owner sends "POST /api/items/yt:abc123xxxxx/subtitles/import" with:
      """
      { "path": "/tmp/broken.srt", "lang": "en" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "line": 2 }
      """
