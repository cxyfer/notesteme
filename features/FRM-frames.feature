@M3
Feature: Key frames
  As the owner, I want key frames picked from a video by rules or by a vision
  model, and as an agent I want to list and fetch them.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:frm001xxxxx" with:
      | field    | value             |
      | title    | Designing a Cache |
      | duration | 360               |
    And "yt:frm001xxxxx" has manual captions in "en"
    And the video of "yt:frm001xxxxx" has the scenes:
      | at       | score | color   |
      | 00:00:05 | 0.90  | #1e3a5f |
      | 00:00:40 | 0.55  | #ffffff |
      | 00:01:15 | 0.80  | #000000 |
      | 00:01:20 | 0.35  | #000000 |
      | 00:02:00 | 0.70  | #3c7d3c |
      | 00:02:40 | 0.45  | #7d3c3c |
      | 00:03:10 | 0.60  | #d0a020 |
      | 00:04:00 | 0.65  | #2050a0 |
      | 00:04:50 | 0.40  | #808080 |
      | 00:05:30 | 0.50  | #a0a0ff |
    And the profile "frm-test" is:
      """
      stages:
        frames: { executor: rules }
      """
    And the default profile is "frm-test"

  @FRM-01 @FRM-R1 @FRM-R4 @FRM-R10
  Scenario: Frames are picked by rules within the range for the video length
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    Then the stage "frames" of "yt:frm001xxxxx" is "done"
    When the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "target": 6, "range": { "min": 5, "max": 8 }, "candidate_count": 9, "selected_count": 6, "shortfall": false }
      """

  @FRM-01 @FRM-R1
  Scenario: Frames are off unless the profile asks for them
    When the owner ingests "yt:frm001xxxxx" with the overrides:
      """
      stages:
        frames: { executor: off }
      """
    And the worker runs until idle
    Then the stage "frames" of "yt:frm001xxxxx" is skipped with the reason "off"
    And the media cache has no files of "yt:frm001xxxxx"

  @FRM-01 @FRM-R1
  Scenario: Audio-only items skip the frames stage
    Given a podcast feed "frmcast" with the episodes:
      | guid  | title      | audio    |
      | ep-42 | Episode 42 | ep42.mp3 |
    And the source "podcast-transcript" returns "ep42-transcript.vtt" for "pod:frmcast/ep-42"
    When the owner ingests "pod:frmcast/ep-42"
    And the worker runs until idle
    Then the stage "frames" of "pod:frmcast/ep-42" is skipped with the reason "audio_only"

  @FRM-01 @FRM-R2 @pending-m0
  Scenario: Quiet stretches still get a frame every interval
    Given a YouTube video "yt:frm002xxxxx" with:
      | field    | value          |
      | title    | A slow lecture |
      | duration | 300            |
    And "yt:frm002xxxxx" has manual captions in "en"
    And the video of "yt:frm002xxxxx" has the scenes:
      | at       | score | color   |
      | 00:00:01 | 0.90  | #101010 |
      | 00:01:00 | 0.10  | #ff0000 |
      | 00:02:00 | 0.10  | #00ff00 |
      | 00:03:00 | 0.10  | #0000ff |
      | 00:04:00 | 0.10  | #ffff00 |
    When the owner ingests "yt:frm002xxxxx"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:frm002xxxxx/frames"
    Then the response contains:
      """
      { "candidate_count": 5, "frames": [
          { "at": "00:00:01.000" }, { "at": "00:01:01.000" }, { "at": "00:02:01.000" },
          { "at": "00:03:01.000" }, { "at": "00:04:01.000" }
      ] }
      """

  @FRM-01 @FRM-R3 @pending-m0
  Scenario: Repeated frames are dropped but different solid colours are kept
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "candidate_count": 9, "frames": [
          { "at": "00:00:05.000" }, { "at": "00:00:40.000" }, { "at": "00:01:15.000" }, { "at": "00:02:00.000" }
      ] }
      """

  @FRM-01 @FRM-R4
  Scenario Outline: The number of frames follows the length of the video
    Given a YouTube video "<ref>" with:
      | field    | value      |
      | title    | Band check |
      | duration | <duration> |
    And "<ref>" has manual captions in "en"
    And the video of "<ref>" has the scenes:
      | at       | score | color   |
      | 00:00:05 | 0.90  | #1e3a5f |
    When the owner ingests "<ref>"
    And the worker runs until idle
    And the owner sends "GET /api/items/<ref>/frames"
    Then the response contains:
      """
      { "target": <target>, "range": { "min": <min>, "max": <max> } }
      """

    Examples:
      | ref            | duration | target | min | max |
      | yt:frmbnd01xxx | 360      | 6      | 5   | 8   |
      | yt:frmbnd02xxx | 1200     | 12     | 8   | 12  |
      | yt:frmbnd03xxx | 2700     | 16     | 12  | 16  |
      | yt:frmbnd04xxx | 5400     | 20     | 16  | 20  |

  @FRM-01 @FRM-R4
  Scenario: A static video gets fewer frames instead of padding
    Given a YouTube video "yt:frm003xxxxx" with:
      | field    | value        |
      | title    | Talking head |
      | duration | 360          |
    And "yt:frm003xxxxx" has manual captions in "en"
    And the video of "yt:frm003xxxxx" has the scenes:
      | at       | score | color   |
      | 00:00:01 | 0.90  | #404040 |
    When the owner ingests "yt:frm003xxxxx"
    And the worker runs until idle
    Then the stage "frames" of "yt:frm003xxxxx" is "done"
    And "yt:frm003xxxxx" shows the message "只找到 1 張有意義的畫面，少於建議的 5 張。"
    When the owner sends "GET /api/items/yt:frm003xxxxx/frames"
    Then the response contains:
      """
      { "selected_count": 1, "shortfall": true }
      """

  @FRM-02 @FRM-R5 @pending-m0
  Scenario: A cue that points at the screen lifts the nearby frame
    Given the "corrected" transcript of "yt:frm001xxxxx" is:
      | # | start    | end      | text                                                    |
      | 1 | 00:00:06 | 00:00:10 | Let us talk about caches.                               |
      | 2 | 00:04:48 | 00:04:53 | As you can see in this diagram, the cache sits in front. |
    When the owner reruns "yt:frm001xxxxx" from "frames"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "frames": [ {}, {}, {}, {}, {}, {}, {}, { "at": "00:04:50.000", "selected": true } ] }
      """

  @FRM-02 @FRM-R5 @FRM-R6
  Scenario: Frames inside a sponsor segment are never picked
    Given SponsorBlock has the segments for "yt:frm001xxxxx":
      | category | start    | end      |
      | sponsor  | 00:03:00 | 00:03:30 |
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "selected_count": 6, "frames": [
          { "at": "00:00:05.000", "selected": true },
          { "at": "00:00:40.000", "selected": true },
          { "at": "00:01:15.000", "selected": true },
          { "at": "00:02:00.000", "selected": true },
          { "at": "00:02:40.000", "selected": false },
          { "at": "00:03:10.000", "selected": false, "excluded": "sponsor" },
          { "at": "00:04:00.000", "selected": true },
          { "at": "00:04:50.000", "selected": false },
          { "at": "00:05:30.000", "selected": true }
      ] }
      """

  @FRM-02 @FRM-R7
  Scenario: A vision model picks frames from numbered contact sheets
    Given the profile "frm-vision" is:
      """
      stages:
        frames: { executor: api, provider: anthropic, model: fixture-model, prompt: frames-default }
      """
    And the default profile is "frm-vision"
    And the LLM provider "anthropic" answers "frames" with "frames-pick-six"
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 1 request for "frames"
    And request 1 to "anthropic" contains "image/jpeg"
    When the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "selected_count": 6, "frames": [ { "at": "00:00:05.000", "selected": true, "caption": "Title slide: Designing a Cache" } ] }
      """

  @FRM-02 @FRM-R7
  Scenario: A pick that is not on the contact sheet is retried
    Given the profile "frm-vision" is:
      """
      stages:
        frames: { executor: api, provider: anthropic, model: fixture-model, prompt: frames-default }
      """
    And the default profile is "frm-vision"
    And the LLM provider "anthropic" answers "frames" with "frames-pick-missing" and then "frames-pick-six"
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 2 requests for "frames"
    And request 2 to "anthropic" contains "picks[0]：第 1 張拼圖沒有編號 12"

  @FRM-02 @FRM-R7 @FRM-R11
  Scenario: Picks that never validate fall back to the rules
    Given the profile "frm-vision" is:
      """
      stages:
        frames: { executor: api, provider: anthropic, model: fixture-model, prompt: frames-default }
      """
    And the default profile is "frm-vision"
    And the LLM provider "anthropic" answers "frames" with "frames-pick-missing"
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    Then the stage "frames" of "yt:frm001xxxxx" is "done"
    And "yt:frm001xxxxx" shows the message "選圖的模型回傳了不存在的編號，已改用規則選圖。"
    And "yt:frm001xxxxx" offers the actions "重新選圖"
    And the "frames" artifact of "yt:frm001xxxxx" has the provenance:
      | field         | value          |
      | frames.select | rules_fallback |

  @FRM-01 @FRM-R8
  Scenario Outline: Frames use the best quality up to 1080p without upscaling
    Given the best video format of "yt:frm001xxxxx" is "<source>"
    When the owner ingests "yt:frm001xxxxx"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "frames": [ { "width": <width>, "height": <height> } ] }
      """
    And the "frames" artifact of "yt:frm001xxxxx" has the provenance:
      | field              | value              |
      | source_resolution  | <source>           |
      | extract_resolution | <width>x<height>   |

    Examples:
      | source    | width | height |
      | 3840x2160 | 1920  | 1080   |
      | 1920x1080 | 1920  | 1080   |
      | 1280x720  | 1280  | 720    |

  @FRM-01 @FRM-R9
  Scenario: Selected frames carry a caption and their chapter
    Given the chapters of "yt:frm001xxxxx" are:
      | start    | title         |
      | 00:00:00 | Why cache     |
      | 00:02:00 | Eviction      |
    And the "corrected" transcript of "yt:frm001xxxxx" is:
      | # | start    | end      | text                                   |
      | 1 | 00:00:04 | 00:00:07 | Caches trade memory for latency.       |
      | 2 | 00:02:00 | 00:02:03 | Least recently used is the classic one. |
    When the owner reruns "yt:frm001xxxxx" from "frames"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response contains:
      """
      { "frames": [
          { "at": "00:00:05.000", "caption": "Caches trade memory for latency.", "chapter": "Why cache" },
          {}, {},
          { "at": "00:02:00.000", "caption": "Least recently used is the classic one.", "chapter": "Eviction" }
      ] }
      """

  @FRM-03 @FRM-R10
  Scenario: A frame image can be downloaded
    Given the item "yt:frm001xxxxx" has been processed up to "frames"
    When the owner sends "GET /api/items/yt:frm001xxxxx/frames/1?size=thumb"
    Then the response status is 200
    And the response header "Content-Type" contains "image/jpeg"

  @FRM-03 @FRM-R10
  Scenario: Items without frames say so
    Given the item "yt:frm001xxxxx" has been processed up to "transcript"
    When the owner sends "GET /api/items/yt:frm001xxxxx/frames"
    Then the response status is 404
    And the response contains:
      """
      { "code": "no_frames" }
      """

  @FRM-01 @FRM-R11
  Scenario: Only the current frames are kept
    Given the item "yt:frm001xxxxx" has been processed up to "frames"
    When the owner reruns "yt:frm001xxxxx" from "frames"
    And the worker runs until idle
    Then the "frames" artifact of "yt:frm001xxxxx" has 1 version
    And the "frames" artifact of "yt:frm001xxxxx" has the provenance:
      | field                  | value |
      | stage                  | frames |
      | frames.select          | rules  |
      | frames.scene_threshold | 0.3    |
      | frames.min_interval_s  | 60     |

  @mcp @FRM-03 @FRM-R12
  Scenario: Agents list the selected frames and fetch one as an image
    Given a token "reader" with scopes "read"
    And the item "yt:frm001xxxxx" has been processed up to "frames"
    When the MCP client with the token "reader" calls "list_frames" with:
      """
      { "item": "yt:frm001xxxxx" }
      """
    Then the tool result contains:
      """
      { "frames": [ { "n": 1, "at": "00:00:05.000" } ] }
      """
    When the MCP client with the token "reader" calls "get_frame" with:
      """
      { "item": "yt:frm001xxxxx", "n": 1 }
      """
    Then the tool result has an image of type "image/jpeg"
    And the tool result contains:
      """
      { "n": 1, "at": "00:00:05.000" }
      """

  @mcp @FRM-03 @FRM-R12
  Scenario: Frame tools need the read scope
    Given a token "worker" with scopes "agent"
    And a token "reader" with scopes "read"
    When the MCP client with the token "worker" lists the tools
    Then the listed names do not include "list_frames, get_frame"
    When the MCP client with the token "reader" lists the tools
    Then the listed names include "list_frames, get_frame"
    And the description of the tool "get_frame" contains "不可信的資料"

  @FRM-02 @FRM-R13
  Scenario: Frames can be re-picked without extracting them again
    Given the item "yt:frm001xxxxx" has been processed up to "frames"
    And the LLM provider "anthropic" answers "frames" with "frames-pick-six"
    When the owner sends "POST /api/items/yt:frm001xxxxx/rerun" with:
      """
      { "from": "frames", "reuse_candidates": true,
        "overrides": { "stages": { "frames": { "executor": "api", "provider": "anthropic", "model": "fixture-model", "prompt": "frames-default" } } } }
      """
    And the worker runs until idle
    Then the "frames" artifact of "yt:frm001xxxxx" has the provenance:
      | field                    | value |
      | frames.select            | api   |
      | frames.reused_candidates | true  |

  @ui @manual @FRM-01
  Scenario: The frames tab shows the picks with their captions
    Given the item "yt:frm001xxxxx" has been processed up to "frames"
    When the owner opens the frames tab of "yt:frm001xxxxx"
    Then the contact sheet shows the selected frames with their caption and chapter
    And the owner can switch to all candidates
