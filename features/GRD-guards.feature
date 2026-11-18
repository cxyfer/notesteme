@M1
Feature: Outbound request and path guards
  As the owner, I want every outbound request and local path checked, so that
  hostile URLs and clients cannot reach my network or my files.

  Background:
    Given the owner is signed in

  @GRD-01 @GRD-R2
  Scenario Outline: Feeds pointing into private networks are blocked
    Given the host "feeds.example.com" resolves to "<ip>"
    When the owner resolves "https://feeds.example.com/show.xml"
    Then the response status is 422
    And the response contains:
      """
      { "message": "這個網址指向內部網路，已封鎖。" }
      """
    And no connection was made to "<ip>"

    Examples:
      | ip              |
      | 127.0.0.1       |
      | 10.0.0.5        |
      | 192.168.1.1     |
      | 169.254.169.254 |
      | ::1             |

  @GRD-01 @GRD-R2
  Scenario: DNS rebinding after the first lookup is still blocked
    Given the host "rebind.example.com" resolves to "93.184.216.34" and then "192.168.1.1"
    And a podcast feed "show" with the episodes:
      | guid  | title      | audio                                  |
      | ep-42 | Episode 42 | https://rebind.example.com/ep42.mp3    |
    When the owner sends "POST /api/items" with:
      """
      { "url": "https://feeds.example.com/show.xml", "episodes": ["ep-42"] }
      """
    And the worker runs until idle
    Then no connection was made to "192.168.1.1"
    And the stage "metadata" of "pod:show/ep-42" failed with the error class "permanent"

  @GRD-02 @GRD-R3
  Scenario: An allow-listed private host can be used
    Given the config file contains:
      """
      network:
        allow_private: [rsshub.lan]
      """
    And the host "rsshub.lan" resolves to "192.168.1.20"
    And the URL "http://rsshub.lan/bilibili/user/video/12345" serves "rsshub-feed.xml"
    When the owner resolves "http://rsshub.lan/bilibili/user/video/12345"
    Then the response status is 200

  @GRD-02 @GRD-R3
  Scenario: Cloud metadata cannot be allow-listed
    Given the config file contains:
      """
      network:
        allow_private: [169.254.169.254]
      """
    When the server starts
    Then the server fails to start with "169.254.169.254"

  @GRD-02 @GRD-R3
  Scenario: Cloud metadata stays blocked inside an allow-listed range
    Given the config file contains:
      """
      network:
        allow_private: [169.254.0.0/16]
      """
    And the host "meta.example.com" resolves to "169.254.169.254"
    When the owner resolves "http://meta.example.com/latest/meta-data/"
    Then the response status is 422
    And the response contains:
      """
      { "code": "blocked_address", "message": "這個網址指向內部網路，已封鎖。" }
      """
    And no connection was made to "169.254.169.254"

  @GRD-03 @GRD-R4
  Scenario: A redirect into the private network is blocked
    Given the host "feeds.example.com" resolves to "93.184.216.34"
    And the URL "https://feeds.example.com/show.xml" redirects to "http://192.168.1.1/admin"
    When the owner resolves "https://feeds.example.com/show.xml"
    Then the response status is 422
    And the response contains:
      """
      { "code": "blocked_address" }
      """
    And no connection was made to "192.168.1.1"

  @GRD-03 @GRD-R4
  Scenario: Too many redirects are refused
    Given the URL "https://a.example.com/1" redirects to "https://a.example.com/2"
    And the URL "https://a.example.com/2" redirects to "https://a.example.com/3"
    And the URL "https://a.example.com/3" redirects to "https://a.example.com/4"
    And the URL "https://a.example.com/4" redirects to "https://a.example.com/5"
    And the URL "https://a.example.com/5" redirects to "https://a.example.com/6"
    And the URL "https://a.example.com/6" redirects to "https://a.example.com/feed.xml"
    And the URL "https://a.example.com/feed.xml" serves "rsshub-feed.xml"
    When the owner resolves "https://a.example.com/1"
    Then the response status is 422
    And the response contains:
      """
      { "code": "too_many_redirects", "message": "轉址超過 5 次。" }
      """

  @GRD-03 @GRD-R5
  Scenario: An oversized feed is cut off
    Given the URL "https://feeds.example.com/huge.xml" serves 20000000 bytes
    When the owner resolves "https://feeds.example.com/huge.xml"
    Then the response status is 422
    And the response contains:
      """
      { "code": "response_too_large" }
      """

  @GRD-01 @GRD-R1
  Scenario: Non-HTTP schemes are refused
    Given the URL "https://feeds.example.com/show.xml" serves "rsshub-feed.xml"
    When the owner resolves "ftp://feeds.example.com/show.xml"
    Then the response status is 422
    And no connection was made to "feeds.example.com"

  @GRD-01 @GRD-R1
  Scenario: A redirect to a non-HTTP scheme is refused
    Given the host "feeds.example.com" resolves to "93.184.216.34"
    And the URL "https://feeds.example.com/show.xml" redirects to "ftp://feeds.example.com/show.xml"
    When the owner resolves "https://feeds.example.com/show.xml"
    Then the response status is 422
    And the response contains:
      """
      { "code": "unsupported_scheme", "message": "只支援 http 與 https 網址。" }
      """

  @GRD-01 @GRD-R6
  Scenario: yt-dlp is never asked to open an arbitrary site
    When the owner ingests "https://example.com/video.mp4"
    Then the response status is 422
    And the library contains 0 items

  @GRD-04 @GRD-R7
  Scenario Outline: Paths outside the media folders are refused
    Given the config file contains:
      """
      inbox: /data/inbox
      media_roots: [/media]
      """
    When the owner sends "POST /api/items" with:
      """
      { "path": "<path>" }
      """
    Then the response status is <status>

    Examples:
      | path                        | status |
      | /media/talks/keynote.mp4    | 201    |
      | /data/inbox/talk.mp4        | 201    |
      | /etc/passwd                 | 403    |
      | /media/../etc/passwd        | 403    |
      | /media/link-to-root/etc/passwd | 403 |

  @GRD-04 @GRD-R7
  Scenario: The refusal message explains the rule
    When the owner sends "POST /api/items" with:
      """
      { "path": "/etc/passwd" }
      """
    Then the response contains:
      """
      { "message": "只能新增媒體資料夾中的檔案。" }
      """

  @GRD-04 @GRD-R8 @cli
  Scenario: The CLI in client mode is held to the same rule
    Given the server is running
    When the owner runs "notesteme ingest /etc/passwd"
    Then the command ran in client mode
    And the command exits with 1
    And the command output contains "只能新增媒體資料夾中的檔案。"
