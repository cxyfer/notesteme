@cli
Feature: CLI and data ownership
  As the owner or an agent, I want a CLI that behaves the same with or without a
  running server, with machine-readable output and meaningful exit codes.

  @M1 @CLI-01 @CLI-R1
  Scenario: A second server on the same data directory refuses to start
    Given the server is running
    When the server starts
    Then the server fails to start with "資料目錄已經由另一個 notesteme serve 使用中。"

  @M1 @CLI-01 @CLI-R2
  Scenario: Without a running server the CLI opens the database itself
    Given a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    When the owner runs "notesteme ingest https://www.youtube.com/watch?v=abc123xxxxx --json"
    Then the command ran in standalone mode
    And the command exits with 0
    And the library contains "yt:abc123xxxxx"

  @M1 @CLI-01 @CLI-R2 @CLI-R4
  Scenario: With a running server the CLI goes through it
    Given the server is running
    And the owner is signed in
    And a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And the owner is listening to events
    When the owner runs "notesteme ingest https://www.youtube.com/watch?v=abc123xxxxx"
    Then the command ran in client mode
    And an event is received:
      """
      { "type": "item.created", "item": "yt:abc123xxxxx" }
      """

  @M1 @CLI-03 @CLI-R3
  Scenario: An explicit server address always uses client mode
    Given a token "win" with scopes "read,ingest"
    And the server is running
    When the owner runs "notesteme items ls --server http://127.0.0.1:8080 --token {token:win} --data /nonexistent"
    Then the command ran in client mode
    And the command exits with 0

  @M1 @CLI-02 @CLI-R5
  Scenario: JSON output matches the API
    Given a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And the item "yt:abc123xxxxx" has been processed up to "metadata"
    When the owner runs "notesteme items ls --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "items": [ { "ref": "yt:abc123xxxxx", "title": "PostgreSQL in 100 Seconds" } ] }
      """

  @M1 @CLI-02 @CLI-R6
  Scenario: A bad argument exits with 2
    When the owner runs "notesteme export yt:abc123xxxxx --format docx"
    Then the command exits with 2

  @M1 @CLI-02 @CLI-R6 @CLI-R7
  Scenario: Waiting on an item that needs the owner exits with 3
    Given a Bilibili video "BV1ai411c7mD" with 1 part
    And "bili:BV1ai411c7mD/p1" has auto captions in "zh-Hans"
    And the source "bilibili-ai" requires the cookie "bilibili"
    When the owner runs "notesteme ingest bili:BV1ai411c7mD/p1 --until transcript --wait"
    Then the command exits with 3
    And the command output contains "上傳 cookie"

  @M1 @CLI-02 @CLI-R7
  Scenario: Waiting until the transcript is ready
    Given a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And "yt:abc123xxxxx" has manual captions in "en"
    When the owner runs "notesteme ingest yt:abc123xxxxx --until transcript --wait --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "stages": { "transcript": "done" } }
      """

  @M1 @CLI-02 @CLI-R8
  Scenario: Doctor reports each dependency with a fix
    Given the environment variable "NOTESTEME_FFMPEG" is "/nonexistent/ffmpeg"
    When the owner runs "notesteme doctor --json"
    Then the command exits with 1
    And the command output is JSON containing:
      """
      { "checks": [ { "name": "ffmpeg", "status": "fail" } ] }
      """

  @M1 @CLI-04 @manual
  Scenario: The watch skill only calls the CLI
    Given the watch skill has been rewritten
    When an agent uses the watch skill on a new YouTube URL
    Then the skill runs "notesteme ingest" and "notesteme export" only
    And the agent does not download, transcribe or extract frames by itself

  @M1 @CLI-03 @manual
  Scenario: A Windows agent drives the WSL instance
    Given notesteme serve runs in WSL Docker
    When an agent on Windows runs "notesteme.exe items ls --server http://localhost:8080 --token <token>"
    Then it lists the same items as the Web UI

  @M2 @MCP-01 @MCP-R1
  Scenario: The MCP stdio server works without a running server
    When the owner runs "notesteme mcp --list-tools"
    Then the command ran in standalone mode
    And the command output contains "claim_tasks"
