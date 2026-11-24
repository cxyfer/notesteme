@M4
Feature: Subscriptions
  As the owner, I want to subscribe to channels, playlists, Bilibili collections
  and podcast feeds, so that new content is processed without me, without paying
  for what I do not care about and without getting blocked by the platforms.

  Background:
    Given the YouTube feeds are served from the registered videos
    And the owner is signed in
    And the clock is at "2026-11-20T08:00:00Z"

  # ---------------------------------------------------------------------------
  # SUB-01：建立訂閱與探索
  # ---------------------------------------------------------------------------

  @SUB-01 @SUB-R1 @SUB-R2 @SUB-R3 @SUB-R13
  Scenario: New uploads on a followed channel are picked up
    Given a YouTube video "yt:old001xxxxx" with:
      | field        | value                |
      | title        | Old talk             |
      | published_at | 2026-11-19T10:00:00Z |
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And the clock advances by "3h"
    And a YouTube video "yt:new001xxxxx" with:
      | field        | value                |
      | title        | New talk             |
      | published_at | 2026-11-20T10:30:00Z |
    And the clock advances by "4h"
    And the worker runs until idle
    Then the library contains 1 item
    And the library contains "yt:new001xxxxx"
    And the subscription "talks" has the entries:
      | video          | decision | reason              |
      | yt:old001xxxxx | skipped  | before_subscription |
      | yt:new001xxxxx | created  |                     |
    When the owner sends "GET /api/items/yt:new001xxxxx"
    Then the response contains:
      """
      { "subscription_id": "{subscription:talks}" }
      """

  @SUB-01 @SUB-R13 @SUB-R14
  Scenario: The subscription list counts the new items that are still unread
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And a YouTube video "yt:new001xxxxx" with:
      | field        | value                |
      | title        | First new talk       |
      | published_at | 2026-11-20T09:00:00Z |
    And a YouTube video "yt:new002xxxxx" with:
      | field        | value                |
      | title        | Second new talk      |
      | published_at | 2026-11-20T10:00:00Z |
    And a YouTube video "yt:new003xxxxx" with:
      | field        | value                |
      | title        | Third new talk       |
      | published_at | 2026-11-20T11:00:00Z |
    And the clock advances by "7h"
    And the worker runs until idle
    Then the library contains 3 items
    When the owner sends "POST /api/items/batch" with:
      """
      { "action": "mark_read", "ids": ["yt:new001xxxxx"] }
      """
    And the owner sends "DELETE /api/items/yt:new002xxxxx"
    And the owner sends "GET /api/subscriptions"
    Then the response status is 200
    And the response contains:
      """
      { "subscriptions": [ { "id": "{subscription:talks}", "new_items": 1 } ] }
      """
    When the owner sends "GET /api/subscriptions/{subscription:talks}"
    Then the response contains:
      """
      { "id": "{subscription:talks}", "new_items": 1 }
      """

  @SUB-01 @SUB-R1
  Scenario Outline: Subscription URLs are recognised by their shape
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "<url>" }
      """
    Then the response status is 201
    And the response contains:
      """
      { "kind": "<kind>", "platform": "<platform>", "external_id": "<external_id>", "enabled": true, "schedule": "6h", "backfill": { "mode": "none" } }
      """

    Examples:
      | url                                                          | kind       | platform | external_id                        |
      | https://www.youtube.com/channel/UCfixture000000000000000     | channel    | youtube  | UCfixture000000000000000           |
      | https://www.youtube.com/@fixturechannel                      | channel    | youtube  | @fixturechannel                    |
      | https://www.youtube.com/playlist?list=PL123                  | playlist   | youtube  | PL123                              |
      | https://space.bilibili.com/9/lists/77?type=season            | collection | bilibili | 9:77                               |
      | https://space.bilibili.com/9/channel/collectiondetail?sid=77 | collection | bilibili | 9:77                               |
      | https://Feeds.Example.com:443/show.xml#latest                | feed       | podcast  | https://feeds.example.com/show.xml |

  @SUB-01 @SUB-R1
  Scenario Outline: Single videos and unknown addresses cannot be subscribed to
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "<url>" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "<code>", "message": "<message>" }
      """
    And the number of subscriptions is 0

    Examples:
      | url                                          | code             | message                                                                      |
      | https://www.youtube.com/watch?v=abc123xxxxx  | not_a_collection | 這是單一影片，請直接新增，不需要訂閱。                                       |
      | https://www.bilibili.com/video/BV1co411c7mA  | not_a_collection | 這是單一影片，請直接新增，不需要訂閱。                                       |
      | https://space.bilibili.com/9/video           | unsupported_url  | 無法辨識這個網址。訂閱支援 YouTube 頻道與播放清單、Bilibili 合集與 Podcast RSS。 |
      | ftp://feeds.example.com/show.xml             | unsupported_url  | 無法辨識這個網址。訂閱支援 YouTube 頻道與播放清單、Bilibili 合集與 Podcast RSS。 |

  @SUB-01 @SUB-R1
  Scenario: Subscribing twice to the same source is refused
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000" }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "duplicate_subscription", "message": "已經訂閱了這個來源。", "subscription_id": "{subscription:talks}" }
      """
    And the number of subscriptions is 1

  @SUB-01 @SUB-R1 @SUB-R12
  Scenario: A handle that turns out to be an existing channel is turned off
    Given the subscription "by-id" for "https://www.youtube.com/channel/UCfixture000000000000000"
    And the subscription "by-handle" for "https://www.youtube.com/@fixturechannel"
    And the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:by-handle}"
    Then the response contains:
      """
      { "external_id": "UCfixture000000000000000", "enabled": false, "last_error_class": "permanent", "last_error": "已經訂閱了這個來源。" }
      """

  @SUB-01 @SUB-R2 @SUB-R5
  Scenario: A Bilibili collection is followed through yt-dlp
    Given a Bilibili video "BV1co411c7mA" with 1 part
    And a Bilibili video "BV1co411c7mB" with 1 part
    And the URL "https://space.bilibili.com/9/lists/77?type=season" serves "sub-bili-collection.json"
    And the subscription "bili" for "https://space.bilibili.com/9/lists/77?type=season" with:
      """
      backfill: { mode: all }
      """
    When the worker runs until idle
    Then the library contains 2 items
    And the library contains "bili:BV1co411c7mA/p1"
    And the library contains "bili:BV1co411c7mB/p1"

  @SUB-01 @SUB-R2 @SUB-R3
  Scenario: A podcast subscription only picks up episodes that appear later
    Given a podcast feed "subshow" with the episodes:
      | guid | title     | audio    |
      | ep-1 | Episode 1 | ep41.mp3 |
    And the subscription "pod" for "https://feeds.example.com/subshow.xml"
    When the worker runs until idle
    And the podcast feed "subshow" adds the episode "ep-2" titled "Episode 2" with the audio "ep42.mp3"
    And the clock advances by "7h"
    And the worker runs until idle
    Then the library contains 1 item
    And the library contains "pod:subshow/ep-2"
    And the subscription "pod" has the entries:
      | video            | decision | reason              |
      | pod:subshow/ep-1 | skipped  | before_subscription |
      | pod:subshow/ep-2 | created  |                     |

  # ---------------------------------------------------------------------------
  # SUB-02：回補範圍、過濾、不加回
  # ---------------------------------------------------------------------------

  @SUB-02 @SUB-R3
  Scenario: Recent backfill takes only the newest videos
    Given a YouTube video "yt:bf0001xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-17T10:00:00Z |
    And a YouTube video "yt:bf0002xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-18T10:00:00Z |
    And a YouTube video "yt:bf0003xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-19T10:00:00Z |
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      backfill: { mode: recent, count: 2 }
      """
    When the worker runs until idle
    Then the library contains 2 items
    And the subscription "talks" has the entries:
      | video          | decision | reason         |
      | yt:bf0001xxxxx | skipped  | backfill_limit |
      | yt:bf0002xxxxx | created  |                |
      | yt:bf0003xxxxx | created  |                |

  @SUB-02 @SUB-R3
  Scenario: Backfilling everything is capped and spread over several checks
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      subscriptions:
        backfill_max: 2
        backfill_per_check: 1
      """
    And a YouTube video "yt:bf0001xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-17T10:00:00Z |
    And a YouTube video "yt:bf0002xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-18T10:00:00Z |
    And a YouTube video "yt:bf0003xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-19T10:00:00Z |
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      backfill: { mode: all }
      """
    When the worker runs until idle
    Then the subscription "talks" has the entries:
      | video          | decision | reason             |
      | yt:bf0001xxxxx | skipped  | backfill_limit     |
      | yt:bf0002xxxxx | deferred | backfill_per_check |
      | yt:bf0003xxxxx | created  |                    |
    When the clock advances by "6h"
    And the worker runs until idle
    Then the library contains 2 items
    And the subscription "talks" has the entries:
      | video          | decision | reason         |
      | yt:bf0001xxxxx | skipped  | backfill_limit |
      | yt:bf0002xxxxx | created  |                |
      | yt:bf0003xxxxx | created  |                |

  @SUB-02 @SUB-R3
  Scenario Outline: Invalid backfill settings name the field at fault
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000", "backfill": <backfill> }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "<path>" } ] }
      """

    Examples:
      | backfill                          | path           |
      | { "mode": "recent", "count": 0 }  | backfill.count |
      | { "mode": "recent", "count": 201 }| backfill.count |
      | { "mode": "everything" }          | backfill.mode  |

  @SUB-02 @SUB-R3
  Scenario: The backfill count error is explained
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000", "backfill": { "mode": "recent", "count": 0 } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "backfill.count", "message": "最近幾部必須是 1 到 200 之間的整數。" } ] }
      """

  @SUB-02 @SUB-R2 @SUB-R4
  Scenario: Filters skip videos by length, Shorts and title
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      filters:
        min_duration_s: 300
        max_duration_s: 5400
        exclude_shorts: true
        title_exclude: "抽獎|直播預告"
      """
    When the worker runs until idle
    And the clock advances by "1h"
    And a YouTube video "yt:short01xxxx" with:
      | field        | value                |
      | title        | Quick tip            |
      | duration     | 50                   |
      | shorts       | yes                  |
      | published_at | 2026-11-20T08:30:00Z |
    And a YouTube video "yt:tiny001xxxx" with:
      | field        | value                |
      | title        | Teaser               |
      | duration     | 120                  |
      | published_at | 2026-11-20T08:31:00Z |
    And a YouTube video "yt:long001xxxx" with:
      | field        | value                |
      | title        | Full conference day  |
      | duration     | 6000                 |
      | published_at | 2026-11-20T08:32:00Z |
    And a YouTube video "yt:promo01xxxx" with:
      | field        | value                |
      | title        | 週末抽獎活動         |
      | duration     | 600                  |
      | published_at | 2026-11-20T08:33:00Z |
    And a YouTube video "yt:keep001xxxx" with:
      | field        | value                |
      | title        | How B-trees work     |
      | duration     | 900                  |
      | published_at | 2026-11-20T08:34:00Z |
    And the clock advances by "7h"
    And the worker runs until idle
    Then the library contains 1 item
    And the library contains "yt:keep001xxxx"
    And the subscription "talks" has the entries:
      | video          | decision | reason         |
      | yt:short01xxxx | skipped  | shorts         |
      | yt:tiny001xxxx | skipped  | too_short      |
      | yt:long001xxxx | skipped  | too_long       |
      | yt:promo01xxxx | skipped  | title_excluded |
      | yt:keep001xxxx | created  |                |

  @SUB-02 @SUB-R4
  Scenario: A required title word skips the videos without it
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      filters:
        title_include: "(?i)postgres"
      """
    When the worker runs until idle
    And the clock advances by "1h"
    And a YouTube video "yt:pg0001xxxxx" with:
      | field        | value                |
      | title        | PostgreSQL vacuum    |
      | published_at | 2026-11-20T08:30:00Z |
    And a YouTube video "yt:my0001xxxxx" with:
      | field        | value                |
      | title        | MySQL replication    |
      | published_at | 2026-11-20T08:31:00Z |
    And the clock advances by "7h"
    And the worker runs until idle
    Then the subscription "talks" has the entries:
      | video          | decision | reason             |
      | yt:pg0001xxxxx | created  |                    |
      | yt:my0001xxxxx | skipped  | title_not_included |

  @SUB-02 @SUB-R4
  Scenario Outline: Invalid filters are rejected with the field and the reason
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000", "filters": <filters> }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "<path>", "message": "<message>" } ] }
      """
    And the number of subscriptions is 0

    Examples:
      | filters                                              | path                  | message                            |
      | { "title_exclude": "(" }                             | filters.title_exclude | 標題的正規表示式無效。             |
      | { "title_exclude": "(?=sponsor)" }                   | filters.title_exclude | 標題的正規表示式無效。             |
      | { "title_include": "(?<!x)y" }                      | filters.title_include | 標題的正規表示式無效。             |
      | { "title_include": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" } | filters.title_include | 標題的正規表示式最長 200 個字元。 |
      | { "min_duration_s": 600, "max_duration_s": 300 }     | filters.max_duration_s | 最長片長必須大於最短片長。         |

  @SUB-02 @SUB-R5
  Scenario: Deleted, trashed and existing videos are not added again
    Given a YouTube video "yt:gone01xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-17T10:00:00Z |
    And a YouTube video "yt:trsh01xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-18T10:00:00Z |
    And a YouTube video "yt:have01xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-19T10:00:00Z |
    And the item "yt:gone01xxxxx" has been ingested
    And the owner sends "DELETE /api/items/yt:gone01xxxxx"
    And the owner sends "DELETE /api/trash/yt:gone01xxxxx"
    And the item "yt:trsh01xxxxx" has been ingested
    And the owner sends "DELETE /api/items/yt:trsh01xxxxx"
    And the item "yt:have01xxxxx" has been ingested
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      backfill: { mode: all }
      """
    When the worker runs until idle
    Then the subscription "talks" has the entries:
      | video          | decision | reason             |
      | yt:gone01xxxxx | skipped  | tombstoned         |
      | yt:trsh01xxxxx | skipped  | in_trash           |
      | yt:have01xxxxx | skipped  | already_in_library |
    And the library contains 1 item
    And the trash contains "yt:trsh01xxxxx"
    When the owner sends "GET /api/items/yt:have01xxxxx"
    Then the response contains:
      """
      { "subscription_id": null }
      """

  @SUB-02 @SUB-R5
  Scenario: A video seen in several checks is added once
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And a YouTube video "yt:new001xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-20T09:00:00Z |
    And the clock advances by "6h"
    And the worker runs until idle
    And the clock advances by "6h"
    And the worker runs until idle
    Then the YouTube feed of the channel "UCfixture000000000000000" was requested 3 times
    And the library contains 1 item
    And the subscription "talks" has the entries:
      | video          | decision | reason |
      | yt:new001xxxxx | created  |        |

  # ---------------------------------------------------------------------------
  # SUB-03：試跑預覽
  # ---------------------------------------------------------------------------

  @SUB-03 @SUB-R6
  Scenario: A dry run lists what would be processed and skipped without creating anything
    Given a YouTube video "yt:keep001xxxx" with:
      | field        | value                |
      | title        | How B-trees work     |
      | duration     | 900                  |
      | published_at | 2026-11-19T10:00:00Z |
    And a YouTube video "yt:long001xxxx" with:
      | field        | value                |
      | title        | Full conference day  |
      | duration     | 6000                 |
      | published_at | 2026-11-18T10:00:00Z |
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      backfill: { mode: recent, count: 5 }
      filters: { max_duration_s: 5400 }
      """
    When the owner sends "POST /api/subscriptions/{subscription:talks}/preview"
    Then the response status is 200
    And the response contains:
      """
      { "would_process": [ { "ref": "yt:keep001xxxx", "title": "How B-trees work", "duration_s": 900 } ],
        "would_skip":    [ { "ref": "yt:long001xxxx", "reason": "too_long" } ] }
      """
    And the library contains 0 items
    And the subscription "talks" has no entries

  @SUB-03 @SUB-R6
  Scenario: Unsaved filters and new subscriptions can be previewed
    Given a YouTube video "yt:keep001xxxx" with:
      | field        | value                |
      | title        | How B-trees work     |
      | published_at | 2026-11-19T10:00:00Z |
    When the owner sends "POST /api/subscriptions/preview" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000",
        "backfill": { "mode": "recent", "count": 5 },
        "filters": { "title_exclude": "B-trees" } }
      """
    Then the response status is 200
    And the response contains:
      """
      { "would_skip": [ { "ref": "yt:keep001xxxx", "reason": "title_excluded" } ] }
      """
    And the number of subscriptions is 0
    And the library contains 0 items

  @SUB-03 @SUB-R6
  Scenario: A dry run with no backfill shows the existing videos as older than the subscription
    Given a YouTube video "yt:keep001xxxx" with:
      | field        | value                |
      | published_at | 2026-11-19T10:00:00Z |
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the owner sends "POST /api/subscriptions/{subscription:talks}/preview"
    Then the response contains:
      """
      { "would_process": [], "would_skip": [ { "ref": "yt:keep001xxxx", "reason": "before_subscription" } ] }
      """

  # ---------------------------------------------------------------------------
  # SUB-04：匯入
  # ---------------------------------------------------------------------------

  @SUB-04 @SUB-R7
  Scenario: An OPML file creates each feed once
    Given the subscription "existing" for "https://feeds.example.com/c.xml"
    And the file "/tmp/podcasts.opml" contains:
      """
      <?xml version="1.0" encoding="UTF-8"?>
      <opml version="2.0">
        <head><title>My podcasts</title></head>
        <body>
          <outline type="rss" text="Show A" xmlUrl="https://feeds.example.com/a.xml"/>
          <outline type="rss" text="Show B" xmlUrl="https://feeds.example.com/b.xml"/>
          <outline type="rss" text="Show A again" xmlUrl="https://feeds.example.com/a.xml"/>
          <outline type="rss" text="Show C" xmlUrl="https://feeds.example.com/c.xml"/>
          <outline type="rss" text="Bad" xmlUrl="ftp://feeds.example.com/d.xml"/>
        </body>
      </opml>
      """
    When the owner imports the subscriptions from "/tmp/podcasts.opml"
    Then the response status is 200
    And the response contains:
      """
      { "created": 2,
        "duplicates": [ "https://feeds.example.com/a.xml", "https://feeds.example.com/c.xml" ],
        "failed": [ { "url": "ftp://feeds.example.com/d.xml" } ] }
      """
    And the number of subscriptions is 3

  @SUB-04 @SUB-R7
  Scenario: A Google Takeout file creates channel subscriptions without backfill
    Given the file "/tmp/subscriptions.csv" contains:
      """
      Channel Id,Channel Url,Channel Title
      UCfixture000000000000000,http://www.youtube.com/channel/UCfixture000000000000000,Fixture Channel
      UCsecond0000000000000000,http://www.youtube.com/channel/UCsecond0000000000000000,Second Channel
      """
    When the owner imports the subscriptions from "/tmp/subscriptions.csv"
    Then the response status is 200
    And the response contains:
      """
      { "created": 2, "duplicates": [], "failed": [] }
      """
    When the owner sends "GET /api/subscriptions"
    Then the response contains:
      """
      { "subscriptions": [
          { "kind": "channel", "external_id": "UCfixture000000000000000", "title": "Fixture Channel", "backfill": { "mode": "none" } },
          { "kind": "channel", "external_id": "UCsecond0000000000000000", "title": "Second Channel",  "backfill": { "mode": "none" } }
      ] }
      """

  @SUB-04 @SUB-R7
  Scenario Outline: Import files over the limits are refused as a whole
    When the owner imports a generated OPML file with <feeds> feeds
    Then the response status is <status>
    And the response contains:
      """
      { "code": "<code>", "message": "<message>" }
      """
    And the number of subscriptions is 0

    Examples:
      | feeds | status | code      | message                                  |
      | 501   | 422    | too_many  | 匯入檔最多 500 個訂閱，這個檔案有 501 個。 |
      | 9000  | 413    | too_large | 匯入檔超過大小上限（1 MiB）。             |

  @SUB-04 @SUB-R7 @SUB-R11
  Scenario: An import file with a document type declaration is refused
    Given the file "/tmp/evil.opml" contains:
      """
      <?xml version="1.0"?>
      <!DOCTYPE opml [ <!ENTITY a "aaaaaaaaaa"> <!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;"> ]>
      <opml version="2.0"><body><outline type="rss" text="&b;" xmlUrl="https://feeds.example.com/a.xml"/></body></opml>
      """
    When the owner imports the subscriptions from "/tmp/evil.opml"
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_format", "message": "無法辨識匯入檔的格式。支援 OPML 與 Google Takeout 的 subscriptions.csv。" }
      """
    And the number of subscriptions is 0

  # ---------------------------------------------------------------------------
  # SUB-05：直播與首播
  # ---------------------------------------------------------------------------

  @SUB-05 @SUB-R8
  Scenario: A premiere from a subscription waits for its recording
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And a YouTube video "yt:prem01xxxxx" with:
      | field        | value                |
      | live_status  | is_upcoming          |
      | published_at | 2026-11-20T09:00:00Z |
    And the clock advances by "7h"
    And the worker runs until idle
    Then the library contains "yt:prem01xxxxx"
    And the stage "metadata" of "yt:prem01xxxxx" is "waiting"
    And the subscription "talks" has the entries:
      | video          | decision | reason |
      | yt:prem01xxxxx | created  |        |

  @SUB-05 @SUB-R8
  Scenario: Live streams and their replays can be skipped
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      filters: { live: skip }
      """
    When the worker runs until idle
    And a YouTube video "yt:live01xxxxx" with:
      | field        | value                |
      | live_status  | is_live              |
      | published_at | 2026-11-20T09:00:00Z |
    And a YouTube video "yt:repl01xxxxx" with:
      | field        | value                |
      | live_status  | was_live             |
      | published_at | 2026-11-20T09:05:00Z |
    And the clock advances by "7h"
    And the worker runs until idle
    Then the library contains 0 items
    And the subscription "talks" has the entries:
      | video          | decision | reason |
      | yt:live01xxxxx | skipped  | live   |
      | yt:repl01xxxxx | skipped  | live   |

  # ---------------------------------------------------------------------------
  # SUB-06：排程、隨機延遲與 rate limit
  # ---------------------------------------------------------------------------

  @SUB-06 @SUB-R9
  Scenario: Without a random delay the next check is exactly one interval later
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    Then the YouTube feed of the channel "UCfixture000000000000000" was requested 1 time
    And the next check of the subscription "talks" is at "2026-11-20T14:00:00Z"

  @SUB-06 @SUB-R9
  Scenario Outline: The random delay stays within its bound
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      schedule: <schedule>
      """
    When the worker runs until idle
    Then the next check of the subscription "talks" is between "<from>" and "<to>"

    Examples:
      | schedule | from                 | to                   |
      | 6h       | 2026-11-20T14:00:00Z | 2026-11-20T14:30:00Z |
      | 1h       | 2026-11-20T09:00:00Z | 2026-11-20T09:06:00Z |

  @SUB-06 @SUB-R9
  Scenario: A long downtime is caught up with a single check
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And the server shuts down gracefully
    And the clock advances by "30h"
    And the server starts
    And the worker runs until idle
    Then the YouTube feed of the channel "UCfixture000000000000000" was requested 2 times
    And the next check of the subscription "talks" is at "2026-11-21T20:00:00Z"

  @SUB-06 @SUB-R9
  Scenario: Checking now runs the check in the next round
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And the clock advances by "1h"
    And the owner sends "POST /api/subscriptions/{subscription:talks}/check"
    Then the response status is 202
    And the response contains:
      """
      { "next_check_at": "2026-11-20T09:00:00Z" }
      """
    When the worker runs until idle
    Then the YouTube feed of the channel "UCfixture000000000000000" was requested 2 times
    And the next check of the subscription "talks" is at "2026-11-20T15:00:00Z"

  @SUB-06 @SUB-R9
  Scenario: Disabled subscriptions are not checked
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      enabled: false
      """
    When the worker runs until idle
    And the clock advances by "24h"
    And the worker runs until idle
    Then the YouTube feed of the channel "UCfixture000000000000000" was requested 0 times

  @SUB-06 @SUB-R9
  Scenario Outline: The check interval must be between an hour and a week
    When the owner sends "POST /api/subscriptions" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000", "schedule": "<schedule>" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "schedule", "message": "檢查頻率必須介於 1 小時到 7 天之間。" } ] }
      """

    Examples:
      | schedule |
      | 30m      |
      | 169h     |
      | soon     |

  @SUB-06 @SUB-R10
  Scenario: Checks over the platform rate limit wait for the next token
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      subscriptions:
        rate_limits: { youtube: 1/m }
      """
    And the URL "https://www.youtube.com/playlist?list=PL123" serves "playlist-3-videos.json"
    And the URL "https://www.youtube.com/playlist?list=PL456" serves "sub-playlist-2-videos.json"
    And the subscription "first" for "https://www.youtube.com/playlist?list=PL123" with:
      """
      backfill: { mode: all }
      """
    And the subscription "second" for "https://www.youtube.com/playlist?list=PL456" with:
      """
      backfill: { mode: all }
      """
    When the worker runs until idle
    Then the library contains 3 items
    When the owner sends "GET /api/subscriptions/{subscription:second}"
    Then the response contains:
      """
      { "last_error": null, "last_error_class": null, "next_check_at": "2026-11-20T08:01:00Z" }
      """
    When the clock advances by "1m"
    And the worker runs until idle
    Then the library contains 5 items

  @SUB-06 @SUB-R10 @SUB-R12
  Scenario: A platform that answers 429 backs off and reports a retryable error
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the YouTube feed of the channel "UCfixture000000000000000" fails with HTTP 429
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    And the owner is listening to events
    When the worker runs until idle
    Then an event is received:
      """
      { "type": "subscription.check_failed", "subscription": "{subscription:talks}", "class": "retryable" }
      """
    When the owner sends "GET /api/subscriptions/{subscription:talks}"
    Then the response contains:
      """
      { "last_error_class": "retryable", "last_error": "暫時無法檢查訂閱（HTTP 429），稍後會自動重試。", "next_check_at": "2026-11-20T08:15:00Z" }
      """

  # ---------------------------------------------------------------------------
  # SUB-R11：feed 是不可信的輸入
  # ---------------------------------------------------------------------------

  @SUB-01 @SUB-R11 @SUB-R12
  Scenario: A feed pointing into a private network is blocked
    Given the host "feeds.example.com" resolves to "10.0.0.5"
    And the subscription "pod" for "https://feeds.example.com/show.xml"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:pod}"
    Then the response contains:
      """
      { "last_error_class": "permanent", "last_error": "這個網址指向內部網路，已封鎖。" }
      """
    And no connection was made to "10.0.0.5"

  @SUB-01 @SUB-R11
  Scenario: DNS rebinding on a later check is still blocked
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the host "rebind.example.com" resolves to "93.184.216.34" and then "192.168.1.1"
    And the URL "https://rebind.example.com/rebindcast.xml" serves "sub-feed-two-episodes.xml"
    And the subscription "pod" for "https://rebind.example.com/rebindcast.xml"
    When the worker runs until idle
    And the clock advances by "6h"
    And the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:pod}"
    Then the response contains:
      """
      { "last_error_class": "permanent", "last_error": "這個網址指向內部網路，已封鎖。" }
      """
    And no connection was made to "192.168.1.1"

  @SUB-01 @SUB-R11
  Scenario: A private host on the allowlist can be subscribed to
    Given the config file contains:
      """
      network:
        allow_private: [feeds.lan]
      """
    And the host "feeds.lan" resolves to "192.168.1.20"
    And the URL "https://feeds.lan/labcast.xml" serves "sub-feed-two-episodes.xml"
    And the subscription "pod" for "https://feeds.lan/labcast.xml"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:pod}"
    Then the response contains:
      """
      { "last_error": null, "last_checked_at": "2026-11-20T08:00:00Z" }
      """
    And the subscription "pod" has the entries:
      | video              | decision | reason              |
      | pod:labcast/sub-ep-1  | skipped  | before_subscription |
      | pod:labcast/sub-ep-2  | skipped  | before_subscription |

  @SUB-01 @SUB-R11
  Scenario: Titles from a feed are cleaned and entries with unsafe links are dropped
    Given the URL "https://feeds.example.com/hostile.xml" serves "sub-hostile-feed.xml"
    And the subscription "pod" for "https://feeds.example.com/hostile.xml"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:pod}"
    Then the response contains:
      """
      { "title": "Hostile Show" }
      """
    When the owner sends "GET /api/subscriptions/{subscription:pod}/entries"
    Then the response contains:
      """
      { "entries": [ { "title": "Episode 1", "decision": "skipped", "reason": "before_subscription" } ] }
      """
    And the library contains 0 items

  @SUB-01 @SUB-R11 @SUB-R12
  Scenario: An oversized feed is not read
    Given the URL "https://feeds.example.com/huge.xml" serves 11534336 bytes
    And the subscription "pod" for "https://feeds.example.com/huge.xml"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:pod}"
    Then the response contains:
      """
      { "last_error_class": "permanent", "last_error": "回應超過大小上限（10 MiB）。" }
      """
    And the library contains 0 items

  @SUB-01 @SUB-R11 @SUB-R12
  Scenario Outline: Feeds that are not valid RSS or Atom are refused
    Given the URL "https://feeds.example.com/<name>.xml" serves "<fixture>"
    And the subscription "pod" for "https://feeds.example.com/<name>.xml"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:pod}"
    Then the response contains:
      """
      { "last_error_class": "permanent", "last_error": "訂閱來源的內容不是有效的 RSS 或 Atom。" }
      """
    And the logs do not contain "<marker>"

    Examples:
      | name    | fixture           | marker              |
      | broken  | sub-malformed.xml | SUB-MALFORMED-MARK  |
      | doctype | sub-doctype.xml   | SUB-ENTITY-EXPANDED |

  # ---------------------------------------------------------------------------
  # SUB-R12：檢查失敗的分類
  # ---------------------------------------------------------------------------

  @SUB-01 @SUB-R12
  Scenario: A channel feed that no longer exists is a permanent error without backoff
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the YouTube feed of the channel "UCfixture000000000000000" fails with HTTP 404
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:talks}"
    Then the response contains:
      """
      { "last_error_class": "permanent",
        "last_error": "訂閱來源已不存在（HTTP 404）。請確認網址，或刪除這個訂閱。",
        "next_check_at": "2026-11-20T14:00:00Z" }
      """

  @SUB-01 @SUB-R12
  Scenario: A playlist that yt-dlp cannot find is a permanent error
    Given the subscription "gone" for "https://www.youtube.com/playlist?list=PLmissing"
    When the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:gone}"
    Then the response contains:
      """
      { "last_error_class": "permanent", "last_error": "找不到這個頻道、播放清單或合集，或它不是公開的。" }
      """

  @SUB-01 @SUB-R12
  Scenario: A failure is cleared by the next successful check
    Given the config file contains:
      """
      scheduler:
        jitter_max: 0s
      """
    And the YouTube feed of the channel "UCfixture000000000000000" fails with HTTP 503
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    When the worker runs until idle
    And the YouTube feed of the channel "UCfixture000000000000000" recovers
    And the clock advances by "1m"
    And the worker runs until idle
    And the owner sends "GET /api/subscriptions/{subscription:talks}"
    Then the response contains:
      """
      { "last_error": null, "last_error_class": null, "next_check_at": "2026-11-20T14:01:00Z" }
      """

  # ---------------------------------------------------------------------------
  # SUB-07 與介面：MCP、REST scope、刪除、CLI
  # ---------------------------------------------------------------------------

  @SUB-07 @SUB-R13 @mcp
  Scenario: An agent creates and lists subscriptions through MCP
    Given a token "boss" with scopes "read,admin"
    And a token "reader" with scopes "read"
    When the MCP client with the token "boss" calls "create_subscription" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000", "backfill": { "mode": "recent", "count": 3 } }
      """
    Then the tool result contains:
      """
      { "kind": "channel", "external_id": "UCfixture000000000000000", "backfill": { "mode": "recent", "count": 3 } }
      """
    When the MCP client with the token "reader" calls "list_subscriptions" with:
      """
      {}
      """
    Then the tool result contains:
      """
      { "subscriptions": [ { "kind": "channel", "external_id": "UCfixture000000000000000" } ] }
      """

  @SUB-07 @SUB-R13 @mcp
  Scenario: Creating a subscription through MCP needs the admin scope
    Given a token "reader" with scopes "read,ingest"
    When the MCP client with the token "reader" calls "create_subscription" with:
      """
      { "url": "https://www.youtube.com/channel/UCfixture000000000000000" }
      """
    Then the tool call fails with "admin"
    And the number of subscriptions is 0

  @SUB-07 @SUB-R13 @mcp
  Scenario: MCP errors carry the same message as the API
    Given a token "boss" with scopes "read,admin"
    When the MCP client with the token "boss" calls "create_subscription" with:
      """
      { "url": "https://www.youtube.com/watch?v=abc123xxxxx" }
      """
    Then the tool call fails with "這是單一影片，請直接新增，不需要訂閱。"

  @SUB-01 @SUB-R13
  Scenario Outline: Each subscription route needs its scope
    Given a token "t" with scopes "<scopes>"
    When the client sends "<request>" with the token "t"
    Then the response status is <status>

    Examples:
      | scopes      | request                          | status |
      | read        | GET /api/subscriptions           | 200    |
      | read        | POST /api/subscriptions          | 403    |
      | read,ingest | POST /api/subscriptions          | 403    |
      | read,ingest | POST /api/subscriptions/preview  | 403    |
      | read,ingest | POST /api/subscriptions/import   | 403    |
      | admin       | POST /api/subscriptions          | 422    |

  @SUB-01 @SUB-R13
  Scenario: Deleting a subscription keeps the videos it added
    Given a YouTube video "yt:bf0001xxxxx" with:
      | field        | value                |
      | published_at | 2026-11-19T10:00:00Z |
    And the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000" with:
      """
      backfill: { mode: all }
      """
    And the worker runs until idle
    When the owner sends "DELETE /api/subscriptions/{subscription:talks}"
    Then the response status is 204
    And the number of subscriptions is 0
    And the library contains "yt:bf0001xxxxx"
    When the owner sends "GET /api/items/yt:bf0001xxxxx"
    Then the response contains:
      """
      { "subscription_id": null }
      """

  @SUB-01 @SUB-R13 @cli
  Scenario: The CLI adds and lists subscriptions
    When the owner runs "notesteme subs add https://www.youtube.com/channel/UCfixture000000000000000 --backfill recent:2 --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "kind": "channel", "external_id": "UCfixture000000000000000", "backfill": { "mode": "recent", "count": 2 } }
      """
    When the owner runs "notesteme subs ls --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "subscriptions": [ { "url": "https://www.youtube.com/channel/UCfixture000000000000000" } ] }
      """

  @SUB-01 @SUB-R13 @cli
  Scenario: Removing a subscription from a script needs --yes
    Given the subscription "talks" for "https://www.youtube.com/channel/UCfixture000000000000000"
    And the server is running
    When the owner runs "notesteme subs rm {subscription:talks}"
    Then the command exits with 2
    And the number of subscriptions is 1

  # ---------------------------------------------------------------------------
  # UI（docs/ui/pages.md 第 5 節）
  # ---------------------------------------------------------------------------

  @ui @manual @SUB-01 @SUB-06
  Scenario: The subscriptions page lists each subscription with its schedule
    Given the owner follows two YouTube channels and one podcast
    When the owner opens the subscriptions page
    Then each subscription shows its platform, check interval, last and next check, profile and new items
    And a subscription whose last check failed shows the error message

  @ui @manual @SUB-02 @SUB-03
  Scenario: Editing the filters updates the dry run at once
    Given the owner opens the drawer of a YouTube channel subscription
    When the owner sets the length to 5 to 90 minutes and excludes titles matching "抽獎"
    Then the dry run lists the videos that would be processed and the skipped ones with their reasons
    And nothing is added to the library until the changes are saved

  @ui @manual @SUB-01
  Scenario: Pasting a channel address offers to subscribe
    Given the owner opens the add drawer
    When the owner pastes "https://www.youtube.com/@fixturechannel"
    Then the drawer offers "建立訂閱" instead of starting a run
    And creating the subscription shows the reminder "同一個頻道只在一台實例上訂閱，避免重複處理。"

  @ui @manual @SUB-04
  Scenario: Importing a Takeout file reports what was created
    Given the owner has a Google Takeout subscriptions.csv with 40 channels, 2 of them already followed
    When the owner imports it from the subscriptions page
    Then the page reports 38 created and 2 duplicates
