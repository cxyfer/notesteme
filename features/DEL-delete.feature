Feature: Deleting items and the trash
  As the owner, I want deleted items to go to a trash first, and a permanent
  delete to leave nothing behind except my spending records, without touching
  files I own or edited.

  Background:
    Given the server is running
    And the owner is signed in
    And a YouTube video "yt:abc123" titled "PostgreSQL in 100 Seconds"
    And "yt:abc123" has manual captions in "en"

  @M1 @DEL-01 @DEL-R1
  Scenario: A deleted item moves to the trash and disappears from the library
    Given the item "yt:abc123" has been processed up to "transcript"
    When the owner sends "DELETE /api/items/yt:abc123"
    Then the response status is 200
    And the library contains 0 items
    And the trash contains "yt:abc123"
    When the owner sends "GET /api/items/yt:abc123"
    Then the response status is 404
    And the response contains:
      """
      { "code": "in_trash" }
      """

  @M1 @DEL-01 @DEL-R1 @mcp
  Scenario: Agents cannot read an item in the trash
    Given a token "agent" with scopes "read,agent"
    And the item "yt:abc123" has been processed up to "transcript"
    When the owner sends "DELETE /api/items/yt:abc123"
    And the MCP client with the token "agent" calls "get_transcript" with:
      """
      { "item": "yt:abc123", "format": "srt" }
      """
    Then the tool call fails with "in_trash"

  @M1 @DEL-01 @DEL-R2
  Scenario: Deleting stops processing and frees the media cache
    Given the item "yt:abc123" has been ingested
    And the media of "yt:abc123" is cached
    And the worker starts one round
    When the owner sends "DELETE /api/items/yt:abc123"
    Then no task is running
    And the stage "metadata" of "yt:abc123" is "skipped"
    And the media cache has no files of "yt:abc123"

  @M1 @DEL-02 @DEL-R3
  Scenario: The deletion preview shows what will be lost without changing anything
    Given the item "yt:abc123" has been processed up to "transcript"
    When the owner sends "GET /api/items/yt:abc123/deletion-preview"
    Then the response status is 200
    And the response contains:
      """
      { "artifacts": "*", "bytes": "*", "spent_usd": "*", "outputs": "*" }
      """
    And the library contains "yt:abc123"

  @M1 @DEL-01 @DEL-R4 @DEL-R5
  Scenario: A restored item comes back with its subtitles
    Given the item "yt:abc123" has been processed up to "transcript"
    And the owner sends "DELETE /api/items/yt:abc123"
    When the owner sends "POST /api/trash/yt:abc123/restore"
    Then the response status is 200
    And the library contains "yt:abc123"
    When the owner sends "GET /api/items/yt:abc123/subtitles?format=srt"
    Then the response status is 200

  @M1 @DEL-01 @DEL-R5
  Scenario: A step cancelled by the deletion can be retried after restoring
    Given the item "yt:abc123" has been ingested
    And the worker starts one round
    And the owner sends "DELETE /api/items/yt:abc123"
    When the owner sends "POST /api/trash/yt:abc123/restore"
    Then the stage "metadata" of "yt:abc123" is "skipped"
    And "yt:abc123" offers the actions "重試"

  @M1 @DEL-03 @DEL-R4 @DEL-R6
  Scenario: The trash is emptied after 30 days
    Given the clock is at "2026-11-12T10:00:00Z"
    And the item "yt:abc123" has been processed up to "transcript"
    And the owner sends "DELETE /api/items/yt:abc123"
    And the owner is listening to events
    When the clock advances by "696h"
    And the worker runs until idle
    Then the trash contains "yt:abc123"
    When the clock advances by "48h"
    And the worker runs until idle
    Then the trash does not contain "yt:abc123"
    And the data directory has no files of "yt:abc123"
    And an event is received:
      """
      { "type": "item.purged", "ref": "yt:abc123", "title": "PostgreSQL in 100 Seconds" }
      """

  @M1 @DEL-03 @DEL-R6 @DEL-R8
  Scenario: Deleting forever removes the copy of an upload but never the owner's own files
    Given a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "transcript"
    And the owner uploads "talk.m4a"
    And the owner sends "DELETE /api/items/local:lecture.mp4"
    And the owner sends "DELETE /api/items/local:talk.m4a"
    When the owner sends "DELETE /api/trash"
    Then the response status is 200
    And the trash contains 0 items
    And the data directory has no files of "local:lecture.mp4"
    And the data directory has no files of "local:talk.m4a"
    And the file "/data/inbox/lecture.mp4" exists

  @M1 @DEL-03 @DEL-R7
  Scenario: Spending records survive a permanent delete
    Given the item "yt:abc123" has been processed up to "transcript"
    And the cost ledger has an entry of "0.42" USD for "yt:abc123"
    And the owner sends "DELETE /api/items/yt:abc123"
    When the owner sends "DELETE /api/trash/yt:abc123"
    Then the cost ledger has an entry of "0.42" USD titled "PostgreSQL in 100 Seconds"

  @M1 @DEL-05 @DEL-R10
  Scenario: Adding an item that is in the trash offers to restore it
    Given the item "yt:abc123" has been processed up to "transcript"
    And the owner sends "DELETE /api/items/yt:abc123"
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://www.youtube.com/watch?v=abc123" }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "in_trash", "message": "這部影音在垃圾桶中。" }
      """
    And "yt:abc123" offers the actions "還原"

  @M1 @DEL-05 @DEL-R10 @DEL-R11
  Scenario: An item added again after a permanent delete starts fresh
    Given the item "yt:abc123" has been processed up to "transcript"
    And the owner sends "DELETE /api/items/yt:abc123"
    And the owner sends "DELETE /api/trash/yt:abc123"
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://www.youtube.com/watch?v=abc123" }
      """
    Then the response status is 201
    And the library contains "yt:abc123"
    And the item "yt:abc123" has no "transcript" artifact

  @M1 @DEL-05 @DEL-R11
  Scenario: The inbox does not bring back a permanently deleted file
    Given the clock is at "2026-11-12T10:00:00Z"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "transcript"
    And the owner sends "DELETE /api/items/local:lecture.mp4"
    And the owner sends "DELETE /api/trash/local:lecture.mp4"
    When the clock advances by "15s"
    And the worker runs until idle
    Then the library contains 0 items
    And the file "/data/inbox/lecture.mp4" exists

  @M2 @DEL-04 @DEL-R9
  Scenario: Output files are kept by default
    Given the profile "media-server" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en] }
      """
    And the default profile is "media-server"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "publish"
    When the owner sends "DELETE /api/items/local:lecture.mp4"
    Then the response status is 200
    And the file "/data/inbox/lecture.en.srt" exists
    And the file "/data/inbox/lecture.mp4" exists

  @M2 @DEL-04 @DEL-R9
  Scenario: Unedited output files are deleted when the owner asks
    Given the profile "media-server" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en] }
      """
    And the default profile is "media-server"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "publish"
    When the owner sends "DELETE /api/items/local:lecture.mp4" with:
      """
      { "delete_outputs": true }
      """
    Then the response status is 200
    And the file "/data/inbox/lecture.en.srt" does not exist
    And the file "/data/inbox/lecture.mp4" exists

  @M2 @DEL-04 @DEL-R9
  Scenario: An output file edited by hand is never deleted
    Given the profile "media-server" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en] }
      """
    And the default profile is "media-server"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "publish"
    And the file "/data/inbox/lecture.en.srt" contains:
      """
      1
      00:00:01,000 --> 00:00:03,500
      I fixed this line by hand.
      """
    When the owner sends "DELETE /api/items/local:lecture.mp4" with:
      """
      { "delete_outputs": true }
      """
    Then the response contains:
      """
      { "kept_outputs": [ { "path": "/data/inbox/lecture.en.srt", "reason": "modified" } ] }
      """
    And the file "/data/inbox/lecture.en.srt" contains "I fixed this line by hand."

  @M2 @DEL-04 @DEL-R9
  Scenario: Restoring writes deleted output files again
    Given the profile "media-server" is:
      """
      outputs:
        - { target: sidecar, formats: [srt], lang: [en] }
      """
    And the default profile is "media-server"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been processed up to "publish"
    And the owner sends "DELETE /api/items/local:lecture.mp4" with:
      """
      { "delete_outputs": true }
      """
    When the owner sends "POST /api/trash/local:lecture.mp4/restore"
    And the worker runs until idle
    Then the file "/data/inbox/lecture.en.srt" exists
    And the LLM provider "anthropic" received 0 requests

  @M1 @DEL-06 @DEL-R12
  Scenario Outline: Only admin tokens can delete or restore
    Given the item "yt:abc123" has been processed up to "transcript"
    And a token "t" with scopes "<scopes>"
    When the client sends "<request>" with the token "t"
    Then the response status is <status>

    Examples:
      | scopes            | request                     | status |
      | read,agent,ingest | DELETE /api/items/yt:abc123 | 403    |
      | admin             | DELETE /api/items/yt:abc123 | 200    |
      | read              | GET /api/trash              | 200    |
      | read,ingest       | DELETE /api/trash           | 403    |

  @M1 @DEL-06 @DEL-R12 @mcp
  Scenario: MCP offers no tool that deletes
    Given a token "root" with scopes "read,agent,ingest,admin"
    When the MCP client with the token "root" lists the tools
    Then the listed names do not include "delete_item,purge_item,restore_item,empty_trash"

  @M1 @DEL-07 @DEL-R13 @cli
  Scenario: Delete from the CLI
    Given the item "yt:abc123" has been processed up to "transcript"
    When the owner runs "notesteme items rm yt:abc123 --yes --json"
    Then the command exits with 0
    And the trash contains "yt:abc123"
    When the owner runs "notesteme items restore yt:abc123"
    Then the command exits with 0
    And the library contains "yt:abc123"

  @M1 @DEL-07 @DEL-R13 @cli
  Scenario: The CLI refuses to delete without confirmation outside a terminal
    Given the item "yt:abc123" has been processed up to "transcript"
    When the owner runs "notesteme items rm yt:abc123"
    Then the command exits with 2
    And the command output contains "--yes"
    And the library contains "yt:abc123"

  @M1 @DEL-07 @DEL-R13 @cli
  Scenario: A Bilibili reference without a part deletes every part
    Given a Bilibili video "BV1xx411c7mD" with 3 parts
    And the item "bili:BV1xx411c7mD/p1" has been ingested
    And the item "bili:BV1xx411c7mD/p2" has been ingested
    And the item "bili:BV1xx411c7mD/p3" has been ingested
    When the owner runs "notesteme items rm bili:BV1xx411c7mD --yes"
    Then the command exits with 0
    And the trash contains 3 items

  @M1 @DEL-07 @DEL-R13 @cli
  Scenario: Purge skips the trash
    Given the item "yt:abc123" has been processed up to "transcript"
    When the owner runs "notesteme items rm yt:abc123 --purge --yes"
    Then the command exits with 0
    And the trash does not contain "yt:abc123"
    And the data directory has no files of "yt:abc123"

  @M1 @DEL-02 @DEL-R3 @ui @manual
  Scenario: The delete dialog explains the consequences and can be undone
    Given the owner is on the item page of "yt:abc123"
    When the owner chooses "刪除" from the "更多" menu
    Then the dialog shows the number of outputs, the disk space freed and the money already spent
    When the owner confirms
    Then a notice "已移到垃圾桶。" appears with a "復原" button
