@M1
Feature: Backup, health and external tools
  As the owner, I want backups, automatic pre-migration snapshots, health
  endpoints and safe yt-dlp updates, so that the instance can run unattended.

  @OPS-01 @OPS-R1 @cli
  Scenario: Back up while the server is running
    Given the environment variable "NOTESTEME_MASTER_KEY" is "ops-backup-master-key-1"
    And the server is running
    And a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And the item "yt:abc123xxxxx" has been ingested
    When the owner runs "notesteme backup --out /backups"
    Then the command exits with 0
    And the file "/backups/notesteme-backup-latest.tar.gz" exists
    And the file "/backups/notesteme-backup-latest.tar.gz" does not contain "ops-backup-master-key-1"

  @OPS-01 @OPS-R2 @cli
  Scenario: Restore brings the library back
    Given a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And the item "yt:abc123xxxxx" has been ingested
    And the owner runs "notesteme backup --out /backups"
    And a YouTube video "yt:later01xxxx" titled "Added after the backup"
    And the item "yt:later01xxxx" has been ingested
    And the server shuts down gracefully
    When the owner runs "notesteme restore /backups/notesteme-backup-latest.tar.gz"
    Then the command exits with 0
    And the file "/data/backups/pre-restore-20261112T100000Z.db" exists
    And the library contains 1 item
    And the library contains "yt:abc123xxxxx"

  @OPS-01 @OPS-R2 @cli
  Scenario: Restore refuses to run while the server is running
    Given the server is running
    And the file "/backups/old.tar.gz" contains:
      """
      fixture: backup-schema-3
      """
    When the owner runs "notesteme restore /backups/old.tar.gz"
    Then the command exits with 1
    And the command output contains "請先停止 notesteme serve"

  @OPS-02 @OPS-R3
  Scenario: A snapshot is taken before a migration
    Given the data directory was created by schema version 3
    And the current schema version is 4
    When the server starts
    Then the file "/data/backups/pre-migration-3.db" exists

  @OPS-02 @OPS-R3
  Scenario: A failed migration restores the snapshot
    Given the data directory was created by schema version 3
    And the current schema version is 4
    And migration 4 fails
    When the server starts
    Then the server fails to start with "migration 4"
    And the database is at schema version 3

  @OPS-02 @OPS-R4
  Scenario: A database newer than the program is refused
    Given the data directory was created by schema version 5
    And the current schema version is 4
    When the server starts
    Then the server fails to start with "pre-migration"

  @OPS-03 @OPS-R5
  Scenario: Readiness reports what is broken
    Given the environment variable "NOTESTEME_FFMPEG" is "/nonexistent/ffmpeg"
    And the server is running
    When the client sends "GET /readyz"
    Then the response status is 503
    And the response contains:
      """
      { "failed": ["ffmpeg"] }
      """
    When the client sends "GET /healthz"
    Then the response status is 200

  @OPS-04 @OPS-R6
  Scenario: A yt-dlp update that fails the self-check is rolled back
    Given the server is running
    And the owner is signed in
    And yt-dlp "2026.11.01" is installed
    And the yt-dlp release "2026.11.10" fails the self-check
    When the owner sends "POST /api/tools/yt-dlp/update"
    Then the response contains:
      """
      { "version": "2026.11.01", "rolled_back": true, "message": "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。" }
      """
    And yt-dlp "2026.11.01" is the active version

  @OPS-04 @OPS-R6
  Scenario: A healthy yt-dlp update is kept
    Given the server is running
    And the owner is signed in
    And yt-dlp "2026.11.01" is installed
    And the yt-dlp release "2026.11.10" passes the self-check
    When the owner sends "POST /api/tools/yt-dlp/update"
    Then the response contains:
      """
      { "version": "2026.11.10", "rolled_back": false }
      """
    And yt-dlp "2026.11.10" is the active version

  @OPS-04 @OPS-R7 @cli
  Scenario: A pinned yt-dlp is not updated
    Given yt-dlp "2026.11.01" is installed
    And the yt-dlp release "2026.11.10" passes the self-check
    When the owner runs "notesteme tools update --pin 2026.11.01"
    And the owner runs "notesteme tools update"
    Then the command output contains "已固定在 2026.11.01"

  @OPS-04 @OPS-R8
  Scenario: The config file path wins over the environment and PATH
    Given the config file contains:
      """
      tools:
        ffmpeg: /opt/ffmpeg/bin/ffmpeg
      """
    And the environment variable "NOTESTEME_FFMPEG" is "/usr/local/bin/ffmpeg"
    And the server is running
    And the owner is signed in
    When the owner sends "GET /api/system"
    Then the response contains:
      """
      { "tools": { "ffmpeg": { "path": "/opt/ffmpeg/bin/ffmpeg", "source": "config_file" } } }
      """

  @OPS-05 @OPS-R9
  Scenario: The WSL compose profile binds to localhost only
    When the owner runs "docker compose -f deploy/docker/compose.yaml --profile wsl config --format json"
    Then the command output is JSON containing:
      """
      { "services": { "notesteme": { "ports": [ { "host_ip": "127.0.0.1" } ] } } }
      """

  @OPS-03 @OPS-R10
  Scenario: Old events are cleaned up
    Given the clock is at "2026-11-12T10:00:00Z"
    And the server is running
    And an event from "2026-08-01T00:00:00Z" exists
    And an event from "2026-09-20T00:00:00Z" exists
    And an event from "2026-11-10T00:00:00Z" exists
    When the clock advances by "24h"
    And the worker runs until idle
    Then no event older than 90 days remains
    And the number of events is 2

  @OPS-03 @OPS-R10
  Scenario: Events are cleaned up at most once a day
    Given the clock is at "2026-11-12T10:00:00Z"
    And the server is running
    And the worker runs until idle
    And an event from "2026-08-01T00:00:00Z" exists
    When the clock advances by "1h"
    And the worker runs until idle
    Then the number of events is 1
    When the clock advances by "24h"
    And the worker runs until idle
    Then the number of events is 0
