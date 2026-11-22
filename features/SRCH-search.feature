@M4
Feature: Full-text search in transcripts
  As the owner, I want to search inside every transcript,
  so that I can find the video where something was said.

  Background:
    Given the owner is signed in
    And the search library has "yt:srch01xxxxx" titled "Stream Processing Talk" with the "corrected" transcript:
      | # | start    | end      | text                                           |
      | 1 | 00:00:01 | 00:00:04 | Today we compare Kafka or Spark for streaming. |
      | 2 | 00:00:04 | 00:00:08 | Kafka keeps an ordered log.                    |
      | 3 | 00:00:08 | 00:00:12 | Spark runs micro-batches.                      |
    And the search library has "yt:srch02xxxxx" titled "資料庫入門" with the "corrected" transcript in "zh-Hant":
      | # | start    | end      | text                 |
      | 1 | 00:00:02 | 00:00:05 | 今天介紹資料庫的索引。 |
      | 2 | 00:00:05 | 00:00:09 | 索引讓查詢變快。       |
      | 3 | 00:00:09 | 00:00:12 | 我在𠮷野家討論索引。   |
      | 4 | 00:00:12 | 00:00:15 | 我們也會用 Kafka。     |

  @SRCH-01 @SRCH-R3 @SRCH-R6
  Scenario: A phrase is found with its timestamp, cue number and highlight
    When the search runs for "ordered log"
    Then the response status is 200
    And the response contains:
      """
      { "total": 1,
        "results": [
          { "item": { "ref": "yt:srch01xxxxx", "title": "Stream Processing Talk" },
            "lang": "en", "variant": "corrected", "cue": 2,
            "start": "00:00:04.000", "end": "00:00:08.000",
            "text": "Kafka keeps an ordered log.",
            "highlights": [ { "start": 15, "end": 26 } ],
            "before": "Today we compare Kafka or Spark for streaming.",
            "after": "Spark runs micro-batches." } ] }
      """

  @SRCH-01 @SRCH-R3 @SRCH-R6
  Scenario: Two-character Chinese words are found by scanning, with UTF-16 offsets
    When the search runs for "索引"
    Then the response contains:
      """
      { "total": 3,
        "results": [
          { "item": { "ref": "yt:srch02xxxxx" }, "lang": "zh-Hant", "cue": 1, "highlights": [ { "start": 8, "end": 10 } ] },
          { "item": { "ref": "yt:srch02xxxxx" }, "lang": "zh-Hant", "cue": 2, "highlights": [ { "start": 0, "end": 2 } ] },
          { "item": { "ref": "yt:srch02xxxxx" }, "lang": "zh-Hant", "cue": 3, "highlights": [ { "start": 8, "end": 10 } ] } ] }
      """

  @SRCH-01 @SRCH-R3
  Scenario: Longer Chinese queries use the trigram index
    When the search runs for "資料庫"
    Then the response contains:
      """
      { "total": 1,
        "results": [ { "item": { "ref": "yt:srch02xxxxx" }, "cue": 1, "highlights": [ { "start": 4, "end": 7 } ] } ] }
      """

  @SRCH-01 @SRCH-R3 @SRCH-R7
  Scenario: Search ignores case and lists newer items first, then cues in order
    When the search runs for "KAFKA"
    Then the response contains:
      """
      { "total": 3,
        "results": [
          { "item": { "ref": "yt:srch02xxxxx" }, "cue": 4, "highlights": [ { "start": 6, "end": 11 } ] },
          { "item": { "ref": "yt:srch01xxxxx" }, "cue": 1, "highlights": [ { "start": 17, "end": 22 } ] },
          { "item": { "ref": "yt:srch01xxxxx" }, "cue": 2, "highlights": [ { "start": 0, "end": 5 } ] } ] }
      """

  @SRCH-01 @SRCH-R4
  Scenario Outline: Query syntax in the input is matched literally
    When the search runs for the query:
      """
      <query>
      """
    Then the response status is 200
    And the response contains:
      """
      { "total": <total> }
      """

    Examples:
      | query              | total |
      | kafka OR spark     | 1     |
      | kafka AND spark    | 0     |
      | NEAR(kafka spark)  | 0     |
      | text_fold:kafka    | 0     |
      | kafka*             | 0     |
      | ^kafka             | 0     |
      | -batches           | 1     |
      | "kafka             | 0     |
      | kafka" OR "spark   | 0     |
      | (kafka)            | 0     |

  @SRCH-01 @SRCH-R4 @SRCH-R6
  Scenario: A literal match of operator words is highlighted as text
    When the search runs for "kafka OR spark"
    Then the response contains:
      """
      { "results": [ { "item": { "ref": "yt:srch01xxxxx" }, "cue": 1, "highlights": [ { "start": 17, "end": 31 } ] } ] }
      """

  @SRCH-01 @SRCH-R5
  Scenario Outline: Bad queries are refused with a message
    When the owner sends "GET /api/search?mode=fulltext&q=<q>"
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "message": "<message>" }
      """

    Examples:
      | q       | message                        |
      | %20%20  | 請輸入要搜尋的文字。           |
      | a%01b   | 搜尋文字含有無法使用的字元。   |

  @SRCH-01 @SRCH-R5
  Scenario: Queries longer than 200 characters are refused
    When the search runs for the query:
      """
      aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "message": "搜尋文字最多 200 個字。" }
      """

  @SRCH-01 @SRCH-R5
  Scenario: Results are paged with a cursor and a bounded limit
    When the owner sends "GET /api/search?mode=fulltext&q=kafka&limit=2"
    Then the response contains:
      """
      { "total": 3, "next_cursor": "*" }
      """
    When the owner sends "GET /api/search?mode=fulltext&q=kafka&limit=101"
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "errors": [ { "path": "limit" } ] }
      """

  @SRCH-01 @SRCH-R1
  Scenario: Without corrected subtitles the most processed variant is indexed
    Given the search library has "yt:srch03xxxxx" titled "Raw Captions" with the "normalized" transcript:
      | # | start    | end      | text                 |
      | 1 | 00:00:01 | 00:00:03 | the quick brown fox  |
    When the search runs for "brown fox"
    Then the response contains:
      """
      { "total": 1, "results": [ { "item": { "ref": "yt:srch03xxxxx" }, "variant": "normalized", "cue": 1 } ] }
      """

  @SRCH-01 @SRCH-R1
  Scenario: Only one variant per language is indexed
    Given the search library has "yt:srch03xxxxx" titled "Raw Captions" with the "normalized" transcript:
      | # | start    | end      | text                 |
      | 1 | 00:00:01 | 00:00:03 | the quick brown fox  |
    And the "corrected" transcript of "yt:srch03xxxxx" is:
      | # | start    | end      | text                 |
      | 1 | 00:00:01 | 00:00:03 | The quick red fox.   |
    When the search runs for "brown fox"
    Then the response contains:
      """
      { "total": 0 }
      """
    When the search runs for "red fox"
    Then the response contains:
      """
      { "total": 1, "results": [ { "variant": "corrected" } ] }
      """

  @SRCH-01 @SRCH-R1 @SRCH-R9
  Scenario: Translations are indexed in their own language and can be filtered
    Given the "translated" transcript of "yt:srch01xxxxx" in "zh-Hant" is:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:04 | 今天我們比較 Kafka 和 Spark。 |
      | 2 | 00:00:04 | 00:00:08 | Kafka 保存有序的日誌。     |
      | 3 | 00:00:08 | 00:00:12 | Spark 執行微批次。         |
    When the owner sends "GET /api/search?mode=fulltext&q=有序的日誌&lang=zh-Hant"
    Then the response contains:
      """
      { "total": 1,
        "results": [ { "item": { "ref": "yt:srch01xxxxx" }, "lang": "zh-Hant", "variant": "translated", "cue": 2 } ] }
      """
    When the owner sends "GET /api/search?mode=fulltext&q=有序的日誌&lang=en"
    Then the response contains:
      """
      { "total": 0 }
      """

  @SRCH-01 @SRCH-R2
  Scenario: A new corrected version replaces the old one in the index
    Given the search library has "yt:srchcorxxxx" titled "Kafka Streams at Scale" with the "segmented" transcript:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:03 | we use cafca streams     |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled  |
    And the "corrected" transcript of "yt:srchcorxxxx" is:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:03 | we use kafkaa streams    |
      | 2 | 00:00:03 | 00:00:06 | with craft mode enabled  |
    And the profile "srch-correct" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default, min_similarity: 0.6 }
      """
    And the LLM provider "anthropic" answers "correct" with "correct-kafka-kraft"
    When the search runs for "kafkaa"
    Then the response contains:
      """
      { "total": 1 }
      """
    When the owner sends "POST /api/items/yt:srchcorxxxx/rerun" with:
      """
      { "from": "correct", "profile": "srch-correct" }
      """
    And the worker runs until idle
    And the search runs for "kafkaa"
    Then the response contains:
      """
      { "total": 0 }
      """
    When the search runs for "cafca"
    Then the response contains:
      """
      { "total": 0 }
      """
    When the search runs for "kafka streams"
    Then the response contains:
      """
      { "total": 1, "results": [ { "item": { "ref": "yt:srchcorxxxx" }, "variant": "corrected", "text": "We use Kafka Streams." } ] }
      """

  @SRCH-01 @SRCH-R8
  Scenario: Items in the trash are left out until restored
    When the owner sends "DELETE /api/items/yt:srch01xxxxx"
    And the search runs for "ordered log"
    Then the response contains:
      """
      { "total": 0 }
      """
    When the owner sends "POST /api/trash/yt:srch01xxxxx/restore"
    And the search runs for "ordered log"
    Then the response contains:
      """
      { "total": 1 }
      """

  @SRCH-01 @SRCH-R8
  Scenario: Purging an item removes it from the index
    When the owner sends "DELETE /api/items/yt:srch01xxxxx"
    And the owner sends "DELETE /api/trash/yt:srch01xxxxx"
    Then the search index has no rows for "yt:srch01xxxxx"
    And the search index has rows for "yt:srch02xxxxx"

  @SRCH-01 @SRCH-R9
  Scenario: Simplified and traditional characters are not folded together
    When the search runs for "资料库"
    Then the response status is 200
    And the response contains:
      """
      { "total": 0 }
      """

  @SRCH-01 @SRCH-R10
  Scenario: Unknown search modes are refused
    When the owner sends "GET /api/search?mode=semantic&q=kafka"
    Then the response status is 422
    And the response contains:
      """
      { "code": "unsupported_mode", "message": "不支援這種搜尋方式：semantic。" }
      """

  @SRCH-01 @SRCH-R10
  Scenario: Full-text search needs the read scope
    Given a token "worker" with scopes "agent"
    When the client sends "GET /api/search?mode=fulltext&q=kafka" with the token "worker"
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "read" }
      """

  @SRCH-01 @SRCH-R10 @mcp
  Scenario: The MCP search tool supports full-text mode
    Given a token "reader" with scopes "read"
    When the MCP client with the token "reader" calls "search" with:
      """
      { "query": "ordered log", "mode": "fulltext" }
      """
    Then the tool result contains:
      """
      { "total": 1, "results": [ { "item": { "ref": "yt:srch01xxxxx" }, "cue": 2, "start": "00:00:04.000" } ] }
      """

  @SRCH-01 @SRCH-R10 @cli
  Scenario: The CLI searches transcripts
    When the owner runs "notesteme search micro-batches --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "total": 1, "results": [ { "item": { "ref": "yt:srch01xxxxx" }, "cue": 3, "highlights": [ { "start": 11, "end": 24 } ] } ] }
      """

  @SRCH-01 @SRCH-R6 @ui @manual
  Scenario: Search results jump to the cue in the item page
    Given the owner is on the library page
    When the owner types "ordered log" into the search box and chooses "字幕內容"
    Then each result shows the item title, the time code and the cue with the match highlighted
    And the highlight is drawn from the "highlights" offsets without rendering any HTML from the cue
    When the owner clicks the result
    Then the item page opens with the player at 00:00:04 and the cue 2 scrolled into view
