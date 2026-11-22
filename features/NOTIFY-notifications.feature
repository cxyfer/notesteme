@M4
Feature: Notifications
  As the owner, I want notifications through ntfy, Discord, Telegram or a
  webhook, so that I hear about results where I already am.

  Background:
    Given the notification endpoints are reachable
    And the owner is signed in
    And a YouTube video "yt:ntf01xxxxxx" titled "PostgreSQL in 100 Seconds"
    And "yt:ntf01xxxxxx" has manual captions in "en"

  @NOTIFY-01 @NOTIFY-R1 @NOTIFY-R6
  Scenario: A Telegram notifier is stored and only the last four characters are shown
    When the owner sends "POST /api/notifiers" with:
      """
      { "id": "telegram", "kind": "telegram",
        "config": { "bot_token": "123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789", "chat_id": "42" },
        "events": ["item.failed"] }
      """
    Then the response status is 201
    When the owner sends "GET /api/notifiers"
    Then the response contains:
      """
      { "notifiers": [ { "id": "telegram", "kind": "telegram", "enabled": true,
          "config": { "chat_id": "42", "bot_token_last4": "6789" }, "events": ["item.failed"] } ] }
      """
    And the data directory does not contain "ABCdefGHIjklMNOpqrSTUvwxYZ0123456789"
    And the logs do not contain "ABCdefGHIjklMNOpqrSTUvwxYZ0123456789"

  @NOTIFY-01 @NOTIFY-R1
  Scenario: Managing notifiers needs the admin scope
    Given a token "agent" with scopes "read,ingest,agent"
    When the client sends "POST /api/notifiers" with the token "agent" and:
      """
      { "id": "phone", "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["item.done"] }
      """
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """

  @NOTIFY-01 @NOTIFY-R1
  Scenario: Notifier ids are unique
    Given the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["item.done"] }
      """
    When the owner sends "POST /api/notifiers" with:
      """
      { "id": "phone", "kind": "ntfy", "config": { "topic": "another-topic" }, "events": ["item.done"] }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "notifier_exists" }
      """

  @NOTIFY-01 @NOTIFY-R2
  Scenario Outline: Telegram and Discord only talk to their own hosts
    When the owner sends "POST /api/notifiers" with:
      """
      { "id": "chat", "kind": "<kind>", "config": <config>, "events": ["item.done"] }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "<path>", "message": "<message>" } ] }
      """

    Examples:
      | kind     | config                                                                                                  | path               | message                                                                 |
      | discord  | { "webhook_url": "https://evil.example/api/webhooks/1/abc" }                                            | config.webhook_url | Discord webhook 只能指向 discord.com，或設定檔 notify.allow_hosts 中列出的主機。 |
      | telegram | { "bot_token": "123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789", "chat_id": "42", "api_base": "https://evil.example" } | config.api_base    | Telegram 只能連到 api.telegram.org，或設定檔 notify.allow_hosts 中列出的主機。  |

  @NOTIFY-01 @NOTIFY-R2
  Scenario: A host listed in the config file can stand in for Telegram
    Given the config file contains:
      """
      notify:
        allow_hosts: [tg-proxy.example.com]
      """
    When the owner sends "POST /api/notifiers" with:
      """
      { "id": "chat", "kind": "telegram",
        "config": { "bot_token": "123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789", "chat_id": "42", "api_base": "https://tg-proxy.example.com" },
        "events": ["item.done"] }
      """
    Then the response status is 201

  @NOTIFY-01 @NOTIFY-R3
  Scenario: Unknown events are rejected with the field at fault
    When the owner sends "POST /api/notifiers" with:
      """
      { "id": "phone", "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["item.done", "item.deleted"] }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "events.1", "message": "不認得的事件：item.deleted。" } ] }
      """

  @NOTIFY-01 @NOTIFY-R2 @NOTIFY-R3 @NOTIFY-R4
  Scenario: Each notifier only gets the events it chose
    Given the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner", "token": "tk_ntfy_secret_0001" }, "events": ["item.done"] }
      """
    And the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.failed"] }
      """
    When the owner ingests "yt:ntf01xxxxxx"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received 1 request
    And the last request to the notification endpoint "https://ntfy.sh/" contains:
      """
      { "topic": "notesteme-owner", "title": "完成：PostgreSQL in 100 Seconds" }
      """
    And the last request to the notification endpoint "https://ntfy.sh/" has the header "Authorization" containing "Bearer tk_ntfy_secret_0001"
    And the notification endpoint "https://hooks.example.com/notesteme" received 0 requests

  @NOTIFY-01 @NOTIFY-R3 @NOTIFY-R4 @NOTIFY-R5
  Scenario: A failure reaches the webhook signed with its secret
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.failed"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And a YouTube video "yt:ntf02xxxxxx" titled "Removed video"
    And yt-dlp fails for "yt:ntf02xxxxxx" with "ERROR: [youtube] ntf02xxxxxx: Video unavailable"
    When the owner ingests "yt:ntf02xxxxxx"
    And the worker runs until idle
    Then the last request to the notification endpoint "https://hooks.example.com/notesteme" contains:
      """
      { "event": "item.failed", "stage": "metadata", "error_class": "permanent",
        "item": { "ref": "yt:ntf02xxxxxx", "title": "Removed video", "url": null }, "instance": null }
      """
    And the last request to the notification endpoint "https://hooks.example.com/notesteme" has the header "X-Notesteme-Event" containing "item.failed"
    And the last request to the notification endpoint "https://hooks.example.com/notesteme" is signed with "whsec-0123456789abcdef"

  @NOTIFY-01 @NOTIFY-R6 @NOTIFY-R12
  Scenario: Changing the events keeps the stored secret
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.failed"] }
      """
    When the owner sends "PATCH /api/notifiers/automation" with:
      """
      { "events": ["item.failed", "alert"] }
      """
    Then the response status is 200
    And the response contains:
      """
      { "config": { "url_host": "hooks.example.com", "url_last4": "teme", "secret_last4": "cdef" } }
      """
    When the owner sends "POST /api/notifiers/automation/test"
    Then the response contains:
      """
      { "ok": true, "status": 204 }
      """
    And the last request to the notification endpoint "https://hooks.example.com/notesteme" is signed with "whsec-0123456789abcdef"
    And the data directory does not contain "whsec-0123456789abcdef"

  @NOTIFY-01 @NOTIFY-R7 @NOTIFY-R12
  Scenario: A webhook that points into the private network is blocked
    Given the host "hooks.internal.example" resolves to "10.0.0.5"
    And the notifier "lan" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.internal.example/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    When the owner sends "POST /api/notifiers/lan/test"
    Then the response status is 200
    And the response contains:
      """
      { "ok": false, "error_class": "permanent", "message": "這個網址指向內部網路，已封鎖。" }
      """
    And no connection was made to "10.0.0.5"

  @NOTIFY-01 @NOTIFY-R7
  Scenario: DNS rebinding after the first delivery is still blocked
    Given the host "hooks.example.com" resolves to "93.184.216.63" and then "192.168.1.1"
    And the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    When the owner sends "POST /api/notifiers/automation/test"
    Then the response contains:
      """
      { "ok": true }
      """
    When the owner sends "POST /api/notifiers/automation/test"
    Then the response contains:
      """
      { "ok": false, "message": "這個網址指向內部網路，已封鎖。" }
      """
    And no connection was made to "192.168.1.1"

  @NOTIFY-01 @NOTIFY-R7
  Scenario: Redirects from a notification endpoint are not followed
    Given the URL "https://redirect.example.com/hook" redirects to "http://169.254.169.254/latest/meta-data"
    And the notifier "moved" is:
      """
      { "kind": "webhook", "config": { "url": "https://redirect.example.com/hook", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    When the owner sends "POST /api/notifiers/moved/test"
    Then the response contains:
      """
      { "ok": false, "error_class": "permanent", "message": "通知端點回應了轉址，不跟隨。" }
      """
    And no connection was made to "169.254.169.254"

  @NOTIFY-01 @NOTIFY-R8
  Scenario: A temporary failure is retried with backoff
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    And the notification endpoint "https://hooks.example.com/notesteme" answers HTTP 503 for the next 2 requests
    And the clock is at "2026-11-23T10:00:00Z"
    When the owner ingests "yt:ntf01xxxxxx"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 1 request
    When the clock advances by "1m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 2 requests
    When the clock advances by "4m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 2 requests
    When the clock advances by "1m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 3 requests
    When the owner sends "GET /api/notifications"
    Then the response contains:
      """
      { "notifications": [ { "notifier": "automation", "event": "item.done", "state": "sent", "attempts": 3 } ] }
      """

  @NOTIFY-01 @NOTIFY-R8
  Scenario: A notification is dropped after the sixth failure
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    And the notification endpoint "https://hooks.example.com/notesteme" answers HTTP 503
    And the clock is at "2026-11-23T10:00:00Z"
    When the owner ingests "yt:ntf01xxxxxx"
    And the worker runs until idle
    And the clock advances by "1m"
    And the worker runs until idle
    And the clock advances by "5m"
    And the worker runs until idle
    And the clock advances by "30m"
    And the worker runs until idle
    And the clock advances by "2h"
    And the worker runs until idle
    And the clock advances by "6h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 6 requests
    When the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 6 requests
    When the owner sends "GET /api/notifications?state=dropped"
    Then the response contains:
      """
      { "notifications": [ { "notifier": "automation", "state": "dropped", "attempts": 6, "last_error": "通知端點回應 HTTP 503。" } ] }
      """

  @NOTIFY-01 @NOTIFY-R8
  Scenario: A permanent failure is dropped without retrying
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    And the notification endpoint "https://hooks.example.com/notesteme" answers HTTP 404
    And the clock is at "2026-11-23T10:00:00Z"
    When the owner ingests "yt:ntf01xxxxxx"
    And the worker runs until idle
    And the clock advances by "24h"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 1 request
    When the owner sends "GET /api/notifications?state=dropped"
    Then the response contains:
      """
      { "notifications": [ { "attempts": 1, "last_error": "通知端點回應 HTTP 404。" } ] }
      """

  @NOTIFY-01 @NOTIFY-R8
  Scenario: Retry-After from a rate-limited endpoint is respected
    Given the notifier "discord" is:
      """
      { "kind": "discord", "config": { "webhook_url": "https://discord.com/api/webhooks/1234/abcd-token-xyz" }, "events": ["item.done"] }
      """
    And the notification endpoint "https://discord.com/api/webhooks/1234/abcd-token-xyz" answers HTTP 429 for the next 1 requests
    And the clock is at "2026-11-23T10:00:00Z"
    When the owner ingests "yt:ntf01xxxxxx"
    And the worker runs until idle
    And the clock advances by "1m"
    And the worker runs until idle
    Then the notification endpoint "https://discord.com/api/webhooks/1234/abcd-token-xyz" received 1 request
    When the clock advances by "1m"
    And the worker runs until idle
    Then the notification endpoint "https://discord.com/api/webhooks/1234/abcd-token-xyz" received 2 requests

  @NOTIFY-01 @NOTIFY-R9
  Scenario: Discord messages cannot ping everyone or inject Markdown
    Given a YouTube video "yt:ntf06xxxxxx" titled "@everyone **Free** [nitro](https://evil.example)"
    And "yt:ntf06xxxxxx" has manual captions in "en"
    And the notifier "discord" is:
      """
      { "kind": "discord", "config": { "webhook_url": "https://discord.com/api/webhooks/1234/abcd-token-xyz" }, "events": ["item.done"] }
      """
    When the owner ingests "yt:ntf06xxxxxx"
    And the worker runs until idle
    Then the last request to the notification endpoint "https://discord.com/api/webhooks/1234/abcd-token-xyz" contains:
      """
      { "allowed_mentions": { "parse": [] } }
      """
    And the notification endpoint "https://discord.com/api/webhooks/1234/abcd-token-xyz" received a request containing "\*\*Free\*\* \[nitro\]\(https://evil.example\)"

  @NOTIFY-01 @NOTIFY-R9
  Scenario: Long titles are cut to 100 characters
    Given a YouTube video "yt:ntf07xxxxxx" titled "Part one of a very long video title that keeps going and going well past any sensible push title limit, END-OF-TITLE-XYZ"
    And "yt:ntf07xxxxxx" has manual captions in "en"
    And the notifier "phone" is:
      """
      { "kind": "ntfy", "config": { "topic": "notesteme-owner" }, "events": ["item.done"] }
      """
    When the owner ingests "yt:ntf07xxxxxx"
    And the worker runs until idle
    Then the notification endpoint "https://ntfy.sh/" received a request containing "push title li…"
    And no request to a notification endpoint contains "END-OF-TITLE-XYZ"

  @NOTIFY-01 @NOTIFY-R9
  Scenario: Notifications never carry subtitle text or secrets
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.done"] }
      """
    And the environment variable "NOTESTEME_SUPADATA_API_KEY" is "sd_live_notify_leak_check"
    And the "corrected" transcript of "yt:ntf01xxxxxx" is:
      | # | start    | end      | text                                   |
      | 1 | 00:00:01 | 00:00:04 | The vault password is CUE-SECRET-4242. |
    When the owner reruns "yt:ntf01xxxxxx" from "publish"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 1 request
    And no request to a notification endpoint contains "CUE-SECRET-4242"
    And no request to a notification endpoint contains "sd_live_notify_leak_check"
    And no request to a notification endpoint contains "whsec-0123456789abcdef"

  @NOTIFY-01 @NOTIFY-R10
  Scenario: Repeated failures of the same step are notified once a day
    Given the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.failed"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And a YouTube video "yt:ntf02xxxxxx" titled "Removed video"
    And yt-dlp fails for "yt:ntf02xxxxxx" with "ERROR: [youtube] ntf02xxxxxx: Video unavailable"
    When the owner ingests "yt:ntf02xxxxxx"
    And the worker runs until idle
    And the owner reruns "yt:ntf02xxxxxx" from "metadata"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 1 request
    When the clock advances by "25h"
    And the owner reruns "yt:ntf02xxxxxx" from "metadata"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 2 requests

  @NOTIFY-01 @NOTIFY-R11
  Scenario: A burst of events is spread out by the rate limit
    Given the config file contains:
      """
      notify:
        rate_limit: { count: 2, per: 10m }
      """
    And the notifier "automation" is:
      """
      { "kind": "webhook", "config": { "url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef" }, "events": ["item.failed"] }
      """
    And the clock is at "2026-11-23T10:00:00Z"
    And a YouTube video "yt:ntf03xxxxxx" titled "Gone one"
    And a YouTube video "yt:ntf04xxxxxx" titled "Gone two"
    And a YouTube video "yt:ntf05xxxxxx" titled "Gone three"
    And yt-dlp fails for "yt:ntf03xxxxxx" with "ERROR: [youtube] ntf03xxxxxx: Video unavailable"
    And yt-dlp fails for "yt:ntf04xxxxxx" with "ERROR: [youtube] ntf04xxxxxx: Video unavailable"
    And yt-dlp fails for "yt:ntf05xxxxxx" with "ERROR: [youtube] ntf05xxxxxx: Video unavailable"
    When the owner ingests "yt:ntf03xxxxxx"
    And the owner ingests "yt:ntf04xxxxxx"
    And the owner ingests "yt:ntf05xxxxxx"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 2 requests
    When the clock advances by "10m"
    And the worker runs until idle
    Then the notification endpoint "https://hooks.example.com/notesteme" received 3 requests

  @NOTIFY-01 @NOTIFY-R12 @NOTIFY-R13
  Scenario: A failed test message reports the status without leaking the bot token
    Given the notifier "telegram" is:
      """
      { "kind": "telegram", "config": { "bot_token": "123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789", "chat_id": "42" }, "events": ["alert"] }
      """
    And the notification endpoint "https://api.telegram.org/bot123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789/sendMessage" answers HTTP 401
    When the owner sends "POST /api/notifiers/telegram/test"
    Then the response status is 200
    And the response contains:
      """
      { "ok": false, "error_class": "permanent", "message": "通知端點回應 HTTP 401。" }
      """
    And the notification endpoint "https://api.telegram.org/bot123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789/sendMessage" received a request containing "Notesteme 測試通知"
    And the logs do not contain "ABCdefGHIjklMNOpqrSTUvwxYZ0123456789"

  @NOTIFY-01 @NOTIFY-R12
  Scenario: Testing a notifier that does not exist
    When the owner sends "POST /api/notifiers/nope/test"
    Then the response status is 404

  @ui @manual @NOTIFY-01 @NOTIFY-R3 @NOTIFY-R12
  Scenario: The owner sets up a channel and sends a test message from the settings
    Given the owner opens the "通知" section of the settings
    When the owner adds a Telegram channel, ticks "失敗" and "需要你處理", and saves
    And chooses "傳送測試通知"
    Then the page shows whether the test message was delivered
    And the bot token is shown only as its last four characters
