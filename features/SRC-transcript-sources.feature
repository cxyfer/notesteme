@M1
Feature: Transcript sources and quality
  As the owner, I want the cheapest usable transcript: platform captions first,
  paid sources only when needed, bad results skipped, and clear actions when
  something needs me.

  Background:
    Given the owner is signed in
    And the clock is at "2026-11-12T10:00:00Z"
    And the global settings are:
      """
      language:
        targets: [zh-Hant-TW]
        caption_priority:
          - { lang: original, kind: manual }
          - { lang: original, kind: auto }
          - { lang: zh-Hans, kind: manual }
        allow_platform_translated: false
        fallback: [supadata, groq]
      """

  @SRC-01 @SRC-R1
  Scenario: Manual captions in the original language win over auto captions
    Given a YouTube video "yt:abc123xxxxx" with:
      | field    | value |
      | language | en    |
    And "yt:abc123xxxxx" has manual captions in "en"
    And "yt:abc123xxxxx" has auto captions in "en"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:abc123xxxxx" came from "youtube-manual"
    And the source "supadata" was called 0 times

  @SRC-01 @SRC-R1
  Scenario: A lower entry in the priority list is used when the first ones are missing
    Given a YouTube video "yt:zh001xxxxxx" with:
      | field    | value |
      | language | ja    |
    And "yt:zh001xxxxxx" has manual captions in "zh-Hans"
    When the owner ingests "yt:zh001xxxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:zh001xxxxxx" came from "youtube-manual"
    And the "source" transcript of "yt:zh001xxxxxx" in "zh-Hans" has 120 cues

  @SRC-01 @SRC-R2
  Scenario: Platform machine translations are not used by default
    Given a YouTube video "yt:abc123xxxxx" with:
      | field    | value |
      | language | en    |
    And "yt:abc123xxxxx" has platform-translated captions in "zh-Hans"
    And the source "supadata" returns "supadata-en.json" for "yt:abc123xxxxx"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:abc123xxxxx" came from "supadata"

  @SRC-02 @SRC-R3
  Scenario: Fallback sources are tried in the configured order
    Given a YouTube video "yt:nocap1xxxxx" titled "No captions here"
    And "yt:nocap1xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:nocap1xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:nocap1xxxxx"
    When the owner ingests "yt:nocap1xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the transcript of "yt:nocap1xxxxx" came from "groq"
    And the source "supadata" was called 1 time

  @SRC-03 @SRC-R4 @pending-m0
  Scenario Outline: Unusable results are rejected and the next source is used
    Given a YouTube video "yt:bad001xxxxx" with:
      | field    | value |
      | language | en    |
      | duration | 600   |
    And "yt:bad001xxxxx" has no caption tracks
    And the source "supadata" returns "<fixture>" for "yt:bad001xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:bad001xxxxx"
    When the owner ingests "yt:bad001xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the source "supadata" was rejected for "yt:bad001xxxxx" because "<reason>"
    And the transcript of "yt:bad001xxxxx" came from "groq"

    Examples:
      | fixture                     | reason            |
      | supadata-covers-200s.json   | low_coverage      |
      | supadata-in-german.json     | language_mismatch |
      | supadata-repeating.json     | repetition        |
      | supadata-music-only.json    | music_only        |

  @SRC-02 @SRC-R5
  Scenario: Running out of sources fails with a fix action
    Given a YouTube video "yt:nocap1xxxxx" titled "No captions here"
    And "yt:nocap1xxxxx" has no caption tracks
    And the source "supadata" has nothing for "yt:nocap1xxxxx"
    And the source "groq" has nothing for "yt:nocap1xxxxx"
    When the owner ingests "yt:nocap1xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:nocap1xxxxx" failed with the error class "permanent"
    And "yt:nocap1xxxxx" shows the message "找不到可用的字幕來源。"
    And "yt:nocap1xxxxx" offers the actions "設定語音轉錄服務"

  @SRC-04 @SRC-R6
  Scenario: Wait for YouTube auto captions instead of paying for ASR
    Given a YouTube video "yt:fresh1xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-12T09:30:00Z |
    And the auto captions of "yt:fresh1xxxxx" are not ready yet
    And the source "groq" returns "groq-en.json" for "yt:fresh1xxxxx"
    When the owner ingests "yt:fresh1xxxxx" with the overrides:
      """
      language:
        fallback: [groq]
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:fresh1xxxxx" waits until "2026-11-12T12:00:00Z"
    And "yt:fresh1xxxxx" shows the message "YouTube 還沒產生自動字幕，預計 2 小時後再檢查。"
    And "yt:fresh1xxxxx" offers the actions "現在改用語音轉錄"
    And the source "groq" was called 0 times
    And the task of "transcript" for "yt:fresh1xxxxx" has 0 attempts

  @SRC-04 @SRC-R6
  Scenario: Stop waiting for auto captions after 24 hours
    Given a YouTube video "yt:fresh1xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-12T09:30:00Z |
    And the auto captions of "yt:fresh1xxxxx" are not ready yet
    And the source "groq" returns "groq-en.json" for "yt:fresh1xxxxx"
    When the owner ingests "yt:fresh1xxxxx" with the overrides:
      """
      language:
        fallback: [groq]
      until: transcript
      """
    And the worker runs until idle
    And the clock advances by "25h"
    And the worker runs until idle
    Then the transcript of "yt:fresh1xxxxx" came from "groq"

  @SRC-04 @SRC-R7
  Scenario: A live stream waits for its replay
    Given a YouTube video "yt:live01xxxxx" with:
      | field       | value   |
      | live_status | is_live |
    When the owner ingests "yt:live01xxxxx"
    And the worker runs until idle
    Then the stage "metadata" of "yt:live01xxxxx" is "waiting"

  @SRC-05 @SRC-R8
  Scenario: Bilibili AI captions without a cookie ask for one
    Given a Bilibili video "BV1ai411c7mD" with 1 part
    And "bili:BV1ai411c7mD/p1" has auto captions in "zh-Hans"
    And the source "bilibili-ai" requires the cookie "bilibili"
    When the owner ingests "bili:BV1ai411c7mD/p1" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "bili:BV1ai411c7mD/p1" is blocked with the reason "needs_action"
    And "bili:BV1ai411c7mD/p1" shows the message "這部影片的 AI 字幕需要登入 Bilibili 才能取得。上傳 Bilibili cookie，或改用語音轉錄。"
    And "bili:BV1ai411c7mD/p1" offers the actions "上傳 cookie, 改用語音轉錄"

  @SRC-05 @SRC-R9
  Scenario: A 403 from YouTube suggests updating yt-dlp first
    Given a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And yt-dlp fails for "yt:abc123xxxxx" with "ERROR: unable to download video data: HTTP Error 403: Forbidden"
    When the owner ingests "yt:abc123xxxxx"
    And the worker runs until idle
    Then the stage "metadata" of "yt:abc123xxxxx" is blocked with the reason "needs_action"
    And "yt:abc123xxxxx" shows the message "YouTube 拒絕了下載請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。"
    And "yt:abc123xxxxx" offers the actions "更新 yt-dlp, 上傳 cookie"

  @SRC-05 @SRC-R10
  Scenario: A paid source without an API key asks for the key
    Given the provider "groq" has no API key
    And a YouTube video "yt:nocap1xxxxx" titled "No captions here"
    And "yt:nocap1xxxxx" has no caption tracks
    When the owner ingests "yt:nocap1xxxxx" with the overrides:
      """
      language:
        fallback: [groq]
      until: transcript
      """
    And the worker runs until idle
    Then the stage "transcript" of "yt:nocap1xxxxx" is blocked with the reason "needs_action"
    And "yt:nocap1xxxxx" offers the actions "設定 API key"

  @SRC-08 @SRC-R11
  Scenario: Detect the original language when metadata does not have it
    Given a local file "meetup.mp4" in the inbox
    And the source "groq" returns "groq-no-language-ja.json" for "local:meetup.mp4"
    When the clock advances by "15s"
    And the worker runs until idle
    Then the original language of "local:meetup.mp4" is "ja"

  @SRC-08 @SRC-R11 @wip
  Scenario: Code-switching speech is marked as mixed
    Given a YouTube video "yt:mix001xxxxx" titled "混合語言的分享"
    And "yt:mix001xxxxx" has auto captions in "zh"
    And the source "youtube-auto" returns "auto-zh-en-mixed.json" for "yt:mix001xxxxx"
    When the owner ingests "yt:mix001xxxxx" with the overrides:
      """
      until: normalize
      """
    And the worker runs until idle
    Then the original language of "yt:mix001xxxxx" is "zh"
    And the original language of "yt:mix001xxxxx" is marked as mixed

  @SRC-06 @SRC-R12
  Scenario: Audio above the provider limit is split and stitched back seamlessly
    Given a local file "long-talk.m4a" in the inbox
    And the audio of "local:long-talk.m4a" is "audio-40mb-after-compression.m4a"
    And the source "groq" accepts files up to "25MB"
    And the source "groq" returns "groq-two-chunks.json" for "local:long-talk.m4a"
    When the clock advances by "15s"
    And the worker runs until idle
    Then the source "groq" was called 2 times
    And the "source" transcript of "local:long-talk.m4a" is:
      | #  | start    | end      | text                                |
      | 41 | 00:24:58 | 00:25:02 | and that is where the overlap starts |
      | 42 | 00:25:02 | 00:25:06 | so we remove the duplicate line     |

  @SRC-03 @SRC-R13 @pending-m0
  Scenario: Whisper hallucinations are filtered out
    Given a local file "quiet-ending.m4a" in the inbox
    And the source "groq" returns "groq-hallucinated-tail.json" for "local:quiet-ending.m4a"
    When the clock advances by "15s"
    And the worker runs until idle
    Then the "source" transcript of "local:quiet-ending.m4a" has 87 cues

  @SRC-09 @SRC-R14
  Scenario: A sidecar subtitle file next to a local video is used before ASR
    Given a local file "lecture.mp4" in the inbox
    And a local file "lecture.en.srt" in the inbox
    When the clock advances by "15s"
    And the worker runs until idle
    Then the transcript of "local:lecture.mp4" came from "local-sidecar"
    And the source "groq" was called 0 times

  @SRC-09 @SRC-R14
  Scenario: An embedded subtitle track is used before ASR
    Given a local file "movie-with-subs.mkv" in the inbox
    When the clock advances by "15s"
    And the worker runs until idle
    Then the transcript of "local:movie-with-subs.mkv" came from "local-embedded"

  @SRC-09 @SRC-R14
  Scenario: A podcast transcript tag is used before ASR
    Given a podcast feed "show" with the episodes:
      | guid  | title      | audio    |
      | ep-42 | Episode 42 | ep42.mp3 |
    And the source "podcast-transcript" returns "ep42-transcript.vtt" for "pod:show/ep-42"
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://feeds.example.com/show.xml", "episodes": ["ep-42"] }
      """
    And the worker runs until idle
    Then the transcript of "pod:show/ep-42" came from "podcast-transcript"

  @SRC-07 @SRC-R15
  Scenario: A podcast transcript that does not match the audio length is flagged
    Given a podcast feed "show" with the episodes:
      | guid  | title      | audio                    |
      | ep-43 | Episode 43 | ep43-with-extra-ads.mp3 |
    And the source "podcast-transcript" returns "ep43-transcript.vtt" for "pod:show/ep-43"
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://feeds.example.com/show.xml", "episodes": ["ep-43"] }
      """
    And the worker runs until idle
    Then "pod:show/ep-43" shows the message "時間可能偏移"

  @SRC-07 @SRC-R16
  Scenario: The source transcript records where it came from
    Given a YouTube video "yt:abc123xxxxx" with:
      | field    | value |
      | language | en    |
    And "yt:abc123xxxxx" has auto captions in "en"
    When the owner ingests "yt:abc123xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    Then the "source" artifact of "yt:abc123xxxxx" has the provenance:
      | field                 | value        |
      | source                | youtube-auto |
      | timing                | word         |
      | tool_versions.yt-dlp  | 2026.11.01   |
    And every cue of the "source" transcript of "yt:abc123xxxxx" has the timing "source"

  @SRC-07 @SRC-R17
  Scenario: The item lists every source that was probed and why it was not used
    Given a YouTube video "yt:nocap1xxxxx" titled "No captions here"
    And "yt:nocap1xxxxx" has no caption tracks
    And the source "supadata" returns "supadata-in-german.json" for "yt:nocap1xxxxx"
    And the source "groq" returns "groq-en.json" for "yt:nocap1xxxxx"
    When the owner ingests "yt:nocap1xxxxx" with the overrides:
      """
      until: transcript
      """
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:nocap1xxxxx"
    Then the response contains:
      """
      { "sources": [
          { "source": "youtube-manual", "lang": "en", "state": "unavailable", "reason": "no_track" },
          { "source": "youtube-auto", "lang": "en", "state": "unavailable", "reason": "no_track" },
          { "source": "youtube-manual", "lang": "zh-Hans", "state": "unavailable", "reason": "no_track" },
          { "source": "supadata", "state": "rejected", "reason": "language_mismatch" },
          { "source": "groq", "lang": "en", "state": "used" }
      ] }
      """
