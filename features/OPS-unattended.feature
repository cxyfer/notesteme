@M4
Feature: Unattended operation
  As the owner, I want daily backups, alerts when something quietly stops
  working, and a VPS compose profile with HTTPS, so that the instance can run
  for a week without me.

  @OPS-06 @OPS-R11
  Scenario: A backup is taken every day at the configured time
    Given the backup directory "/backups" contains:
      | file       |
      | README.txt |
    And the config file contains:
      """
      backup:
        daily: { at: "03:00", timezone: UTC, dir: /backups }
      """
    And the environment variable "NOTESTEME_MASTER_KEY" is "ops-daily-master-key-1"
    And the clock is at "2026-11-23T02:30:00Z"
    And the server is running
    When the clock advances by "29m"
    And the worker runs until idle
    Then the file "/backups/notesteme-backup-20261123T030000Z.tar.gz" does not exist
    When the clock advances by "1m"
    And the worker runs until idle
    Then the file "/backups/notesteme-backup-20261123T030000Z.tar.gz" exists
    And the file "/backups/notesteme-backup-latest.tar.gz" exists
    And the file "/backups/notesteme-backup-latest.tar.gz" does not contain "ops-daily-master-key-1"

  @OPS-06 @OPS-R11
  Scenario: Missed daily backups are made up only once
    Given the backup directory "/backups" contains:
      | file       |
      | README.txt |
    And the config file contains:
      """
      backup:
        daily: { at: "03:00", timezone: UTC, dir: /backups }
      """
    And the clock is at "2026-11-23T02:00:00Z"
    And the server is running
    And the server shuts down gracefully
    And the clock advances by "74h"
    When the server starts
    And the worker runs until idle
    Then the backup directory "/backups" contains exactly:
      | file                                       |
      | README.txt                                 |
      | notesteme-backup-20261126T040000Z.tar.gz   |
      | notesteme-backup-latest.tar.gz             |

  @OPS-06 @OPS-R12
  Scenario: Only the newest seven backups made by Notesteme are kept
    Given the backup directory "/backups" contains:
      | file                                     |
      | notesteme-backup-20261116T030000Z.tar.gz |
      | notesteme-backup-20261117T030000Z.tar.gz |
      | notesteme-backup-20261118T030000Z.tar.gz |
      | notesteme-backup-20261119T030000Z.tar.gz |
      | notesteme-backup-20261120T030000Z.tar.gz |
      | notesteme-backup-20261121T030000Z.tar.gz |
      | notesteme-backup-20261122T030000Z.tar.gz |
      | notesteme-backup-before-upgrade.tar.gz   |
      | pre-migration-3.db                       |
      | my-own-archive.tar.gz                    |
    And the config file contains:
      """
      backup:
        daily: { at: "03:00", timezone: UTC, dir: /backups }
      """
    And the clock is at "2026-11-23T02:59:00Z"
    And the server is running
    When the clock advances by "1m"
    And the worker runs until idle
    Then the backup directory "/backups" contains exactly:
      | file                                     |
      | notesteme-backup-20261117T030000Z.tar.gz |
      | notesteme-backup-20261118T030000Z.tar.gz |
      | notesteme-backup-20261119T030000Z.tar.gz |
      | notesteme-backup-20261120T030000Z.tar.gz |
      | notesteme-backup-20261121T030000Z.tar.gz |
      | notesteme-backup-20261122T030000Z.tar.gz |
      | notesteme-backup-20261123T030000Z.tar.gz |
      | notesteme-backup-latest.tar.gz           |
      | notesteme-backup-before-upgrade.tar.gz   |
      | pre-migration-3.db                       |
      | my-own-archive.tar.gz                    |

  @OPS-06 @OPS-07 @OPS-R12 @OPS-R13
  Scenario: A failed daily backup deletes nothing and alerts once
    Given the notification endpoints are reachable
    And the backup directory "/backups" contains:
      | file                                     |
      | notesteme-backup-20261122T030000Z.tar.gz |
    And the file "/backups/not-a-directory" contains:
      """
      this is a file, so the backup cannot be written under it
      """
    And the config file contains:
      """
      backup:
        daily: { at: "03:00", timezone: UTC, dir: /backups/not-a-directory, keep: 1 }
      """
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the clock is at "2026-11-23T02:59:00Z"
    And the server is running
    When the clock advances by "1m"
    And the worker runs until idle
    Then the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "event": "alert", "alert": { "kind": "backup_failed", "resolved": false } }
      """
    And the notification endpoint "https://hooks.example.com/ops" received a request containing "每日備份失敗："
    And the file "/backups/notesteme-backup-20261122T030000Z.tar.gz" exists
    When the clock advances by "1m"
    And the worker runs until idle
    And the clock advances by "5m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 1 request

  @OPS-07 @OPS-R13 @OPS-R15
  Scenario: A scheduled job that has not succeeded for a day is reported
    Given the notification endpoints are reachable
    And the file "/backups/not-a-directory" contains:
      """
      this is a file, so the backup cannot be written under it
      """
    And the config file contains:
      """
      backup:
        daily: { at: "03:00", timezone: UTC, dir: /backups/not-a-directory }
      """
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the clock is at "2026-11-23T02:59:00Z"
    And the server is running
    When the clock advances by "1m"
    And the worker runs until idle
    And the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 2 requests
    And the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "alert": { "kind": "job_stale", "key": "backup.daily:daily" } }
      """
    And the notification endpoint "https://hooks.example.com/ops" received a request containing "排程 backup.daily 超過 24 小時沒有成功執行："

  @OPS-07 @OPS-R13 @OPS-R14
  Scenario: A task without progress is reported once and the recovery is reported too
    Given the notification endpoints are reachable
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the owner is signed in
    And the profile "agent-seg" is:
      """
      stages:
        segment: { executor: agent }
      """
    And the default profile is "agent-seg"
    And a token "agent-token" with scopes "agent"
    And an agent "helper" using the token "agent-token"
    And a YouTube video "yt:ops07xxxxxx" titled "Kafka talk"
    And "yt:ops07xxxxxx" has auto captions in "en"
    And the clock is at "2026-11-23T10:00:00Z"
    When the owner ingests "yt:ops07xxxxxx"
    And the worker runs until idle
    And the clock advances by "5h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 0 requests
    When the clock advances by "2h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received a request containing "「Kafka talk」的 segment 超過 6 小時沒有進展。"
    When the clock advances by "1h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 1 request
    When the agent "helper" claims up to 1 task of kind "segment"
    And the clock advances by "15m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 2 requests
    And the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "alert": { "kind": "stuck_task", "resolved": true } }
      """

  @OPS-07 @OPS-R14
  Scenario: Tasks in a queue the owner paused are not reported
    Given the notification endpoints are reachable
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the owner is signed in
    And a YouTube video "yt:ops08xxxxxx" titled "Paused on purpose"
    And "yt:ops08xxxxxx" has manual captions in "en"
    And the clock is at "2026-11-23T10:00:00Z"
    And the queue is paused
    When the owner ingests "yt:ops08xxxxxx"
    And the clock advances by "12h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 0 requests

  @OPS-07 @OPS-R13 @OPS-R16
  Scenario: Disk usage is reported above the threshold and cleared well below it
    Given the notification endpoints are reachable
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And the data volume is 93 percent full
    And the server is running
    When the worker runs until idle
    Then the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "alert": { "kind": "disk_usage", "key": "/data", "resolved": false, "message": "/data 的磁碟用量是 93%，超過 90%。" } }
      """
    Given the data volume is 88 percent full
    When the clock advances by "15m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 1 request
    Given the data volume is 84 percent full
    When the clock advances by "15m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 2 requests
    And the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "alert": { "kind": "disk_usage", "resolved": true } }
      """

  @OPS-07 @OPS-R13
  Scenario: A single alert can be turned off
    Given the notification endpoints are reachable
    And the config file contains:
      """
      alerts:
        disk_usage_percent: 0
      """
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the data volume is 99 percent full
    And the server is running
    When the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received 0 requests

  @OPS-07 @OPS-R17
  Scenario: A cookie that has not worked for two weeks is reported
    Given the notification endpoints are reachable
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And the cookie for "youtube" was last used successfully at "2026-11-08T09:00:00Z"
    And the server is running
    When the worker runs until idle
    Then the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "alert": { "kind": "cookie_stale", "key": "youtube",
          "message": "YouTube cookie 超過 14 天沒有成功使用，可能已經失效。請重新上傳。" } }
      """

  @OPS-07 @OPS-R18 @pending-m0
  Scenario Outline: A jump in yt-dlp failures is reported only with enough calls
    Given the notification endpoints are reachable
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And yt-dlp recorded <failures> failures out of <calls> calls in the last hour
    And the server is running
    When the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/ops" received <alerts> requests

    Examples:
      | failures | calls | alerts |
      | 4        | 5     | 1      |
      | 3        | 3     | 0      |
      | 2        | 10    | 0      |

  @OPS-07 @OPS-R18 @pending-m0
  Scenario: The yt-dlp alert says what to try
    Given the notification endpoints are reachable
    And the notifier "ops" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/ops", "secret": "whsec-ops-0123456789abcdef" }, "events": ["alert"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And yt-dlp recorded 4 failures out of 5 calls in the last hour
    And the server is running
    When the worker runs until idle
    Then the last request to the notification endpoint "https://hooks.example.com/ops" contains:
      """
      { "alert": { "kind": "ytdlp_failure_rate",
          "message": "最近 1 小時 yt-dlp 失敗 4/5 次。先更新 yt-dlp；如果仍然失敗，上傳 cookie 或確認 VPS 的 IP 是否被封鎖。" } }
      """

  @OPS-08 @OPS-R19
  Scenario: The VPS compose profile adds Caddy with HTTPS in front of the app
    Given the compose .env file contains:
      | field                | value             |
      | NOTESTEME_DOMAIN     | notes.example.com |
      | NOTESTEME_DEPLOYMENT | vps               |
    When the owner runs "docker compose -f deploy/docker/compose.yaml --profile vps config --format json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "services": {
          "caddy": {
            "image": "caddy:2.8.4-alpine",
            "ports": [
              { "target": 80,  "published": "80",  "protocol": "tcp" },
              { "target": 443, "published": "443", "protocol": "tcp" },
              { "target": 443, "published": "443", "protocol": "udp" }
            ],
            "environment": { "NOTESTEME_DOMAIN": "notes.example.com" },
            "depends_on": {
              "vps-check": { "condition": "service_completed_successfully" },
              "notesteme": { "condition": "service_healthy" }
            }
          },
          "notesteme": {
            "ports": [ { "host_ip": "127.0.0.1" } ],
            "environment": { "NOTESTEME_DEPLOYMENT": "vps" }
          }
      } }
      """

  @OPS-08 @OPS-R20 @cli
  Scenario Outline: The VPS check refuses to expose an instance that is not ready
    Given the instance is deployed with the "<deployment>" profile
    And the environment variable "NOTESTEME_PASSWORD" is not set
    And the environment variable "NOTESTEME_DOMAIN" is "<domain>"
    When the owner runs "notesteme deploy-check vps"
    Then the command exits with 1
    And the command output contains "<message>"

    Examples:
      | deployment | domain            | message                                                                |
      | vps        | localhost         | vps profile 需要在 .env 設定 NOTESTEME_DOMAIN（例如 notes.example.com）。 |
      | local      | notes.example.com | vps profile 需要在 .env 設定 NOTESTEME_DEPLOYMENT=vps。                  |
      | vps        | notes.example.com | vps profile 必須設定登入密碼。請在 .env 設定 NOTESTEME_PASSWORD 後重新啟動。 |

  @OPS-08 @OPS-R20 @cli
  Scenario: The VPS check passes when everything is set
    Given the instance is deployed with the "vps" profile
    And the environment variable "NOTESTEME_PASSWORD" is "a-long-enough-owner-password"
    And the environment variable "NOTESTEME_DOMAIN" is "notes.example.com"
    When the owner runs "notesteme deploy-check vps"
    Then the command exits with 0
    And the command output contains "vps 部署檢查通過。"

  @OPS-08 @OPS-R21
  Scenario: A VPS deployment cannot run without authentication
    Given the instance is deployed with the "vps" profile
    And the environment variable "NOTESTEME_PASSWORD" is not set
    When the server starts with the flag "--insecure-no-auth"
    Then the server fails to start with "vps 部署不能使用 --insecure-no-auth。請設定 NOTESTEME_PASSWORD。"

  @OPS-08 @OPS-R21
  Scenario: Only known deployment kinds are accepted
    Given the instance is deployed with the "cloud" profile
    When the server starts
    Then the server fails to start with "NOTESTEME_DEPLOYMENT 只能是 local 或 vps。"

  @OPS-08 @OPS-R21
  Scenario: Session cookies are marked secure on a VPS
    Given the instance is deployed with the "vps" profile
    And the owner password is "correct horse battery staple"
    When someone signs in with the password "correct horse battery staple"
    Then the response status is 200
    And the response header "Set-Cookie" contains "Secure"

  @ui @manual @OPS-07 @OPS-R13
  Scenario: Active alerts are shown on the overview
    Given the disk usage alert is active
    When the owner opens the overview
    Then the alert is listed with its message and since when it has been active
    And notifications that were dropped are listed next to it
