@M3
Feature: SponsorBlock segments
  As the owner, I want sponsor segments left out of my frames and notes,
  while my subtitles stay complete.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:spon01xxxxx" with:
      | field    | value                  |
      | title    | Building a Tiny Kernel |
      | duration | 600                    |
    And "yt:spon01xxxxx" has manual captions in "en"
    And SponsorBlock has the segments for "yt:spon01xxxxx":
      | category  | start    | end      |
      | intro     | 00:00:00 | 00:00:10 |
      | sponsor   | 00:01:00 | 00:01:30 |
      | selfpromo | 00:08:00 | 00:08:20 |

  @SPB-01 @SPB-R1 @SPB-R3 @SPB-R4
  Scenario: Segments of the default categories are stored with the item
    When the owner ingests "yt:spon01xxxxx"
    And the worker runs until idle
    Then the stage "sponsorblock" of "yt:spon01xxxxx" is "done"
    When the owner sends "GET /api/items/yt:spon01xxxxx"
    Then the response contains:
      """
      { "sponsor_segments": [
          { "category": "sponsor",   "start": "00:01:00.000", "end": "00:01:30.000" },
          { "category": "selfpromo", "start": "00:08:00.000", "end": "00:08:20.000" }
      ] }
      """

  @SPB-01 @SPB-R1
  Scenario: Items that are not on YouTube skip SponsorBlock
    Given a Bilibili video "BV1sp4y1c7mD" with 1 part
    And "bili:BV1sp4y1c7mD/p1" has manual captions in "zh-Hans"
    When the owner ingests "bili:BV1sp4y1c7mD/p1"
    And the worker runs until idle
    Then the stage "sponsorblock" of "bili:BV1sp4y1c7mD/p1" is skipped with the reason "not_applicable"
    And SponsorBlock received 0 requests for "yt:spon01xxxxx"

  @SPB-01 @SPB-R1
  Scenario: SponsorBlock can be turned off
    When the owner ingests "yt:spon01xxxxx" with the overrides:
      """
      stages:
        sponsorblock: { enabled: false }
      """
    And the worker runs until idle
    Then the stage "sponsorblock" of "yt:spon01xxxxx" is skipped with the reason "off"
    And SponsorBlock received 0 requests for "yt:spon01xxxxx"

  @SPB-01 @SPB-R2
  Scenario: Only a hash prefix of the video ID is sent to SponsorBlock
    When the owner ingests "yt:spon01xxxxx"
    And the worker runs until idle
    Then SponsorBlock received 1 request for "yt:spon01xxxxx"
    And SponsorBlock never received the video ID of "yt:spon01xxxxx"

  @SPB-01 @SPB-R3
  Scenario: The categories to leave out can be chosen
    When the owner ingests "yt:spon01xxxxx" with the overrides:
      """
      stages:
        sponsorblock: { categories: [intro] }
      """
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:spon01xxxxx"
    Then the response contains:
      """
      { "sponsor_segments": [ { "category": "intro", "start": "00:00:00.000", "end": "00:00:10.000" } ] }
      """

  @SPB-01 @SPB-R3
  Scenario: Unknown categories are rejected with the field at fault
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "bad-spb", "config": { "stages": { "sponsorblock": { "categories": ["sponsor", "ads"] } } } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "stages.sponsorblock.categories.1" } ] }
      """

  @SPB-01 @SPB-R4
  Scenario: A video without segments gets an empty list
    Given a YouTube video "yt:spon02xxxxx" titled "No ads here"
    And "yt:spon02xxxxx" has manual captions in "en"
    When the owner ingests "yt:spon02xxxxx"
    And the worker runs until idle
    Then the stage "sponsorblock" of "yt:spon02xxxxx" is "done"
    When the owner sends "GET /api/items/yt:spon02xxxxx"
    Then the response contains:
      """
      { "sponsor_segments": [] }
      """

  @SPB-01 @SPB-R5 @SPB-R6
  Scenario: A failed lookup counts as no segments and does not block
    Given SponsorBlock fails for "yt:spon01xxxxx" with HTTP 503
    When the owner ingests "yt:spon01xxxxx"
    And the worker runs until idle
    Then the stage "sponsorblock" of "yt:spon01xxxxx" is "done"
    And the stage "chapters" of "yt:spon01xxxxx" is skipped with the reason "not_configured"
    And "yt:spon01xxxxx" shows the message "無法取得 SponsorBlock 片段（HTTP 503），這次當作沒有片段。"
    And "yt:spon01xxxxx" offers the actions "重新查詢 SponsorBlock"
    And the "sponsor_segments" artifact of "yt:spon01xxxxx" has the provenance:
      | field                 | value |
      | sponsorblock.outcome  | error |

  @SPB-01 @SPB-R6
  Scenario: The lookup is recorded in the provenance
    When the owner ingests "yt:spon01xxxxx"
    And the worker runs until idle
    Then the "sponsor_segments" artifact of "yt:spon01xxxxx" has the provenance:
      | field                   | value                                |
      | stage                   | sponsorblock                         |
      | sponsorblock.base_url   | https://sponsor.ajay.app             |
      | sponsorblock.categories | sponsor,selfpromo,interaction        |
      | sponsorblock.outcome    | ok                                   |

  @SPB-01 @SPB-R7
  Scenario: Cues inside a segment stay in the subtitles but not in the note input
    Given the profile "spb-note" is:
      """
      stages:
        note: { executor: api, provider: anthropic, model: fixture-model, prompt: note-default }
      """
    And the default profile is "spb-note"
    And the "corrected" transcript of "yt:spon01xxxxx" is:
      | # | start    | end      | text                                   |
      | 1 | 00:00:20 | 00:00:25 | Today we build a tiny kernel.          |
      | 2 | 00:01:05 | 00:01:10 | This video is sponsored by NordWidget. |
      | 3 | 00:02:00 | 00:02:05 | First we set up the boot loader.       |
    And the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:spon01xxxxx" from "sponsorblock"
    And the worker runs until idle
    Then request 1 to "anthropic" does not contain "NordWidget"
    And request 1 to "anthropic" contains "boot loader"
    And the "corrected" transcript of "yt:spon01xxxxx" has 3 cues

  @SPB-01 @SPB-R8
  Scenario: Rerunning SponsorBlock queries again but keeps an identical result
    Given the item "yt:spon01xxxxx" has been processed up to "sponsorblock"
    When the owner reruns "yt:spon01xxxxx" from "sponsorblock"
    And the worker runs until idle
    Then SponsorBlock received 2 requests for "yt:spon01xxxxx"
    And the "sponsor_segments" artifact of "yt:spon01xxxxx" has 1 version
