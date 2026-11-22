@M4
Feature: A better transcript source after processing
  As the owner, I want to be told when a better transcript source appears after
  I processed a video, so that I can upgrade it.

  Background:
    Given the platform adds manual captions in "en" to "yt:late01xxxxx" at "2026-11-21T00:00:00Z"
    And the owner is signed in
    And the clock is at "2026-11-20T08:00:00Z"
    And the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the global settings are:
      """
      language:
        caption_priority:
          - { lang: original, kind: manual }
          - { lang: original, kind: auto }
        fallback: [groq]
      """
    And a YouTube video "yt:late01xxxxx" with:
      | field        | value                |
      | title        | Late captions        |
      | language     | en                   |
      | published_at | 2026-11-20T06:00:00Z |
    And "yt:late01xxxxx" has auto captions in "en"

  @SRC-10 @SRC-R18 @SRC-R19 @SRC-R21
  Scenario: Manual captions that appear later are reported, not used silently
    Given the item "yt:late01xxxxx" has been processed up to "transcript"
    And the owner is listening to events
    When the clock advances by "24h"
    And the worker runs until idle
    Then an event is received:
      """
      { "type": "source.better_available", "item": "yt:late01xxxxx",
        "current": { "source": "youtube-auto", "lang": "en" },
        "better": { "source": "youtube-manual", "lang": "en", "kind": "manual" },
        "action": "notify" }
      """
    And "yt:late01xxxxx" shows the message "發現更好的字幕來源：YouTube 人工字幕（en）。重跑會再花一次 LLM 費用，目前無法估算金額。"
    And "yt:late01xxxxx" offers the actions "改用更好的字幕重跑"
    And the transcript of "yt:late01xxxxx" came from "youtube-auto"
    And the "source" artifact of "yt:late01xxxxx" has 1 version

  @SRC-10 @SRC-R18
  Scenario: Re-probing is scheduled every 12 hours until seven days after publication
    Given the item "yt:late01xxxxx" has been processed up to "transcript"
    When the owner sends "GET /api/items/yt:late01xxxxx"
    Then the response contains:
      """
      { "reprobe": { "next_at": "2026-11-20T20:00:00Z", "until": "2026-11-27T06:00:00Z", "runs": 0, "sources": true, "sponsorblock": false } }
      """
    When the clock advances by "12h"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:late01xxxxx"
    Then the response contains:
      """
      { "reprobe": { "next_at": "2026-11-21T08:00:00Z", "runs": 1 }, "better_source": null }
      """

  @SRC-10 @SRC-R18
  Scenario: Re-probing stops seven days after publication
    Given the platform adds manual captions in "en" to "yt:slow01xxxxx" at "2026-11-28T00:00:00Z"
    And a YouTube video "yt:slow01xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-20T06:00:00Z |
    And "yt:slow01xxxxx" has auto captions in "en"
    And the item "yt:slow01xxxxx" has been processed up to "transcript"
    When the clock advances by "192h"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:slow01xxxxx"
    Then the response contains:
      """
      { "reprobe": null, "better_source": null }
      """
    And the transcript of "yt:slow01xxxxx" came from "youtube-auto"

  @SRC-10 @SRC-R18
  Scenario: Videos already on the first source in the list are not re-probed
    Given a YouTube video "yt:best01xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-20T06:00:00Z |
    And "yt:best01xxxxx" has manual captions in "en"
    And the item "yt:best01xxxxx" has been processed up to "transcript"
    When the owner sends "GET /api/items/yt:best01xxxxx"
    Then the response contains:
      """
      { "reprobe": null }
      """

  @SRC-10 @SRC-R18
  Scenario: Videos processed more than seven days after publication are not re-probed
    Given a YouTube video "yt:aged01xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-01T06:00:00Z |
    And "yt:aged01xxxxx" has auto captions in "en"
    And the item "yt:aged01xxxxx" has been processed up to "transcript"
    When the owner sends "GET /api/items/yt:aged01xxxxx"
    Then the response contains:
      """
      { "reprobe": null }
      """

  @SRC-10 @SRC-R19
  Scenario: Re-probing only lists the platform captions and never calls a paid source
    Given the platform adds manual captions in "en" to "yt:asr001xxxxx" at "2026-11-21T00:00:00Z"
    And a YouTube video "yt:asr001xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-20T06:00:00Z |
    And "yt:asr001xxxxx" has no caption tracks
    And the source "groq" returns "groq-en.json" for "yt:asr001xxxxx"
    And the item "yt:asr001xxxxx" has been processed up to "transcript"
    When the clock advances by "24h"
    And the worker runs until idle
    Then the source "groq" was called 1 time
    And the transcript of "yt:asr001xxxxx" came from "groq"
    And "yt:asr001xxxxx" shows the message "發現更好的字幕來源：YouTube 人工字幕（en）。重跑會再花一次 LLM 費用，目前無法估算金額。"

  @SRC-10 @SRC-R19
  Scenario: The sources of the item still show the probe of the current transcript
    Given the item "yt:late01xxxxx" has been processed up to "transcript"
    When the clock advances by "24h"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:late01xxxxx"
    Then the response contains:
      """
      { "sources": [
          { "source": "youtube-manual", "lang": "en", "state": "unavailable", "reason": "no_track" },
          { "source": "youtube-auto",   "lang": "en", "state": "used" }
      ] }
      """

  @SRC-10 @SRC-R20
  Scenario: SponsorBlock is queried again while the video is new
    Given a YouTube video "yt:spon03xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-20T06:00:00Z |
    And "yt:spon03xxxxx" has manual captions in "en"
    And the item "yt:spon03xxxxx" has been processed up to "sponsorblock"
    And SponsorBlock has the segments for "yt:spon03xxxxx":
      | category | start    | end      |
      | sponsor  | 00:01:00 | 00:01:30 |
    When the clock advances by "12h"
    And the worker runs until idle
    Then SponsorBlock received 2 requests for "yt:spon03xxxxx"
    And the "sponsor_segments" artifact of "yt:spon03xxxxx" has 2 versions
    And "yt:spon03xxxxx" shows the message "SponsorBlock 片段有更新，從章節重跑才會套用到章節、關鍵畫面與筆記。"
    And "yt:spon03xxxxx" offers the actions "從章節重跑"

  @SRC-10 @SRC-R20
  Scenario: Unchanged SponsorBlock segments do not create a new version
    Given a YouTube video "yt:spon04xxxxx" with:
      | field        | value                |
      | language     | en                   |
      | published_at | 2026-11-20T06:00:00Z |
    And "yt:spon04xxxxx" has manual captions in "en"
    And the item "yt:spon04xxxxx" has been processed up to "sponsorblock"
    When the clock advances by "12h"
    And the worker runs until idle
    Then SponsorBlock received 2 requests for "yt:spon04xxxxx"
    And the "sponsor_segments" artifact of "yt:spon04xxxxx" has 1 version

  @SRC-10 @SRC-R22
  Scenario: A profile can rerun automatically with the better source
    Given the profile "upgrade" is:
      """
      language:
        better_source: rerun
      """
    And the default profile is "upgrade"
    And the item "yt:late01xxxxx" has been processed up to "transcript"
    And the owner is listening to events
    When the clock advances by "24h"
    And the worker runs until idle
    Then an event is received:
      """
      { "type": "source.better_available", "item": "yt:late01xxxxx", "action": "rerun" }
      """
    And the transcript of "yt:late01xxxxx" came from "youtube-manual"
    And the "source" artifact of "yt:late01xxxxx" has 2 versions

  @SRC-10 @SRC-R22
  Scenario: An automatic rerun never replaces a transcript the owner edited
    Given the profile "upgrade" is:
      """
      language:
        better_source: rerun
      """
    And the default profile is "upgrade"
    And the item "yt:late01xxxxx" has been processed up to "transcript"
    And the "edited" transcript of "yt:late01xxxxx" is:
      | # | start        | end          | text                |
      | 1 | 00:00:01.000 | 00:00:03.000 | My own fixed line.  |
    When the clock advances by "24h"
    And the worker runs until idle
    Then the transcript of "yt:late01xxxxx" came from "youtube-auto"
    And the "source" artifact of "yt:late01xxxxx" has 1 version
    And "yt:late01xxxxx" shows the message "發現更好的字幕來源：YouTube 人工字幕（en）。重跑會再花一次 LLM 費用，目前無法估算金額。"

  @SRC-10 @SRC-R22
  Scenario: Re-probing can be turned off
    Given the profile "quiet" is:
      """
      language:
        better_source: "off"
      """
    And the default profile is "quiet"
    And the item "yt:late01xxxxx" has been processed up to "transcript"
    When the clock advances by "24h"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:late01xxxxx"
    Then the response contains:
      """
      { "reprobe": null, "better_source": null }
      """

  @SRC-10 @SRC-R22
  Scenario: An unknown better-source setting is rejected with the field at fault
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "bad-upgrade", "config": { "language": { "better_source": "sometimes" } } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "language.better_source" } ] }
      """

  @SRC-10 @SRC-R23
  Scenario: The better source carries the estimated cost and a manual rerun uses it
    Given the item "yt:late01xxxxx" has been processed up to "transcript"
    When the clock advances by "24h"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:late01xxxxx"
    Then the response contains:
      """
      { "better_source": { "source": "youtube-manual", "lang": "en", "kind": "manual", "found_at": "2026-11-21T08:00:00Z", "estimate_usd": null } }
      """
    When the owner reruns "yt:late01xxxxx" from "transcript"
    And the worker runs until idle
    Then the transcript of "yt:late01xxxxx" came from "youtube-manual"
    When the owner sends "GET /api/items/yt:late01xxxxx"
    Then the response contains:
      """
      { "better_source": null, "reprobe": null }
      """

  @ui @manual @SRC-10 @SRC-R21 @SRC-R23
  Scenario: The item page offers the upgrade with its estimated cost
    Given a processed video for which YouTube manual captions appeared later
    When the owner opens the item page
    Then a reminder says a better transcript source is available and which one
    And choosing "改用更好的字幕重跑" shows the estimated LLM cost before anything runs
