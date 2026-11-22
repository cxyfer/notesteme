@M3
Feature: Notes
  As the owner, I want notes generated from a template where the model only
  writes the body, every heading linked to its moment, and every note checked
  before it reaches my vault.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:note01xxxxx" with:
      | field        | value                |
      | title        | Writing a Toy Kernel |
      | channel      | Kernel Hackers       |
      | duration     | 600                  |
      | published_at | 2025-08-09T10:00:00Z |
    And the profile "note-test" is:
      """
      stages:
        note: { executor: api, provider: anthropic, model: fixture-model, prompt: note-default, template: video-notes }
      language: { targets: [zh-Hant-TW] }
      """
    And the default profile is "note-test"
    And the chapters of "yt:note01xxxxx" are:
      | start    | title      | cue |
      | 00:00:00 | Setting up | 2   |
      | 00:05:00 | Booting    | 4   |
    And the frames of "yt:note01xxxxx" are:
      | # | at       | selected | caption           |
      | 1 | 00:00:20 | yes      | Toolchain diagram |
      | 2 | 00:02:30 | no       |                   |
      | 3 | 00:05:00 | yes      | Boot sequence     |
    And the "corrected" transcript of "yt:note01xxxxx" is:
      | # | start    | end      | text                           |
      | 1 | 00:00:05 | 00:00:10 | Welcome back to the channel.   |
      | 2 | 00:00:20 | 00:00:25 | Today we set up the toolchain. |
      | 3 | 00:02:30 | 00:02:35 | We install a cross compiler.   |
      | 4 | 00:05:00 | 00:05:05 | Now the boot loader.           |
      | 5 | 00:09:00 | 00:09:05 | It jumps into the kernel.      |

  @NOTE-01 @NOTE-R1 @NOTE-R2
  Scenario: The template writes the frontmatter and the model only the body
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the stage "note" of "yt:note01xxxxx" is "done"
    And the note of "yt:note01xxxxx" has the frontmatter:
      | field            | value                                       |
      | title            | Writing a Toy Kernel                        |
      | source           | https://www.youtube.com/watch?v=note01xxxxx |
      | channel          | Kernel Hackers                              |
      | video_published  | 2025-08-09                                  |
      | source_kind      | derived                                     |
      | status           | not-started                                 |
      | read_at          |                                             |
      | reading_status   | ⚪ 待閱讀                                    |
      | notesteme_status | complete                                    |
    And the note of "yt:note01xxxxx" contains "> [!info] 影片資訊"
    And the note of "yt:note01xxxxx" contains "We install a cross compiler first"

  @NOTE-01 @NOTE-R1
  Scenario: Notes can be turned off
    When the owner sends "POST /api/items/yt:note01xxxxx/rerun" with:
      """
      { "from": "note", "overrides": { "stages": { "note": { "executor": "off" } } } }
      """
    And the worker runs until idle
    Then the stage "note" of "yt:note01xxxxx" is skipped with the reason "off"
    And the LLM provider "anthropic" received 0 requests for "note"

  @NOTE-01 @NOTE-R2
  Scenario: A hostile title cannot change the frontmatter
    Given a YouTube video "yt:note04xxxxx" with:
      | field | value                                |
      | title | Kernel: "part 1" #2 --- evil: yes    |
    And the "corrected" transcript of "yt:note04xxxxx" is:
      | # | start    | end      | text                         |
      | 1 | 00:00:05 | 00:00:10 | Welcome back to the channel. |
    And the LLM provider "anthropic" answers "note" with "note-minimal"
    When the owner reruns "yt:note04xxxxx" from "note"
    And the worker runs until idle
    Then the note of "yt:note04xxxxx" has the frontmatter:
      | field | value                             |
      | title | Kernel: "part 1" #2 --- evil: yes |

  @NOTE-01 @NOTE-R2
  Scenario: A note built on a degraded result is marked partial
    Given a YouTube video "yt:note05xxxxx" titled "Sponsor lookup fails"
    And the "corrected" transcript of "yt:note05xxxxx" is:
      | # | start    | end      | text                         |
      | 1 | 00:00:05 | 00:00:10 | Welcome back to the channel. |
    And SponsorBlock fails for "yt:note05xxxxx" with HTTP 503
    And the LLM provider "anthropic" answers "note" with "note-minimal"
    When the owner reruns "yt:note05xxxxx" from "sponsorblock"
    And the worker runs until idle
    Then the note of "yt:note05xxxxx" has the frontmatter:
      | field            | value   |
      | notesteme_status | partial |

  @NOTE-02 @NOTE-R4
  Scenario: Headings and cue references become links to the moment
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the note of "yt:note01xxxxx" contains "## Setting up the toolchain · [00:20](https://www.youtube.com/watch?v=note01xxxxx&t=20s)"
    And the note of "yt:note01xxxxx" contains "### The boot loader · [05:00](https://www.youtube.com/watch?v=note01xxxxx&t=300s)"
    And the note of "yt:note01xxxxx" contains "[09:00](https://www.youtube.com/watch?v=note01xxxxx&t=540s)"
    And the note of "yt:note01xxxxx" does not contain "[[cue:"

  @NOTE-02 @NOTE-R4
  Scenario: Bilibili links use the part and seconds
    Given a Bilibili video "BV1nt4y1c7mD" with 1 part
    And the "corrected" transcript of "bili:BV1nt4y1c7mD/p1" is:
      | # | start    | end      | text                         |
      | 1 | 00:00:05 | 00:00:10 | Welcome back to the channel. |
    And the LLM provider "anthropic" answers "note" with "note-minimal"
    When the owner reruns "bili:BV1nt4y1c7mD/p1" from "note"
    And the worker runs until idle
    Then the note of "bili:BV1nt4y1c7mD/p1" contains "## Overview · [00:05](https://www.bilibili.com/video/BV1nt4y1c7mD?p=1&t=5)"

  @NOTE-02 @NOTE-R4
  Scenario: Local files get plain timestamps
    Given a local file "lecture.mp4" in the inbox
    And the "corrected" transcript of "local:lecture.mp4" is:
      | # | start    | end      | text                         |
      | 1 | 00:00:05 | 00:00:10 | Welcome back to the lecture. |
    And the LLM provider "anthropic" answers "note" with "note-minimal"
    When the owner reruns "local:lecture.mp4" from "note"
    And the worker runs until idle
    Then the note of "local:lecture.mp4" contains "## Overview · 00:05"
    And the note of "local:lecture.mp4" does not contain "· [00:05]"

  @NOTE-02 @NOTE-R3
  Scenario Outline: Invalid note output is retried with the error
    Given the LLM provider "anthropic" answers "note" with "<fixture>" and then "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 2 requests for "note"
    And request 2 to "anthropic" contains "<error>"
    And the stage "note" of "yt:note01xxxxx" is "done"

    Examples:
      | fixture                  | error                                |
      | note-missing-cue         | sections[1].cue 999 不在輸入中         |
      | note-unselected-frame    | sections[0].frames[0] 2 不是選中的畫面 |
      | note-starts-with-level-3 | sections[0].level 必須是 2            |

  @NOTE-02 @NOTE-R3
  Scenario: A note that never validates fails with a fix action
    Given the LLM provider "anthropic" answers "note" with "note-missing-cue"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the stage "note" of "yt:note01xxxxx" failed with the error class "permanent"
    And "yt:note01xxxxx" shows the message "筆記的內容沒有通過驗證（sections[1].cue 999 不在輸入中）。"
    And "yt:note01xxxxx" offers the actions "重新產生筆記, 開啟 Prompt"

  @NOTE-03 @NOTE-R5
  Scenario: The note is written from the corrected original in the note language
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then request 1 to "anthropic" contains "Today we set up the toolchain."
    And request 1 to "anthropic" contains "zh-Hant-TW"
    And request 1 to "anthropic" contains "Toolchain diagram"
    And request 1 to "anthropic" does not contain "00:00:20"
    When the owner sends "GET /api/items/yt:note01xxxxx/note"
    Then the response contains:
      """
      { "lang": "zh-Hant-TW" }
      """

  @NOTE-03 @NOTE-R5
  Scenario: The translation is added to the input only when asked
    Given the "translated" transcript of "yt:note01xxxxx" in "zh-Hant-TW" is:
      | # | start    | end      | text           |
      | 1 | 00:00:05 | 00:00:10 | 歡迎回到頻道。 |
      | 2 | 00:00:20 | 00:00:25 | 今天設定工具鏈。 |
      | 3 | 00:02:30 | 00:02:35 | 安裝交叉編譯器。 |
      | 4 | 00:05:00 | 00:05:05 | 接著是開機載入器。 |
      | 5 | 00:09:00 | 00:09:05 | 它跳進核心。   |
    And the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner sends "POST /api/items/yt:note01xxxxx/rerun" with:
      """
      { "from": "note", "overrides": { "stages": { "note": { "include_translation": true } } } }
      """
    And the worker runs until idle
    Then request 1 to "anthropic" contains "今天設定工具鏈。"
    When the owner sends "POST /api/items/yt:note01xxxxx/rerun" with:
      """
      { "from": "note" }
      """
    And the worker runs until idle
    Then request 2 to "anthropic" does not contain "今天設定工具鏈。"

  @NOTE-03 @NOTE-R5
  Scenario: The note language can be set apart from the targets
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner sends "POST /api/items/yt:note01xxxxx/rerun" with:
      """
      { "from": "note", "overrides": { "stages": { "note": { "lang": "ja" } } } }
      """
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:note01xxxxx/note"
    Then the response contains:
      """
      { "lang": "ja" }
      """

  @NOTE-03 @NOTE-R6
  Scenario: Long videos are summarised per chapter and then combined
    Given a YouTube video "yt:note02xxxxx" with:
      | field    | value             |
      | title    | A Two Hour Course |
      | duration | 7200              |
    And the chapters of "yt:note02xxxxx" are:
      | start    | title      | cue |
      | 00:00:00 | Part one   | 1   |
      | 00:40:00 | Part two   | 2   |
      | 01:20:00 | Part three | 3   |
    And the "corrected" transcript of "yt:note02xxxxx" is:
      | # | start    | end      | text                       |
      | 1 | 00:10:00 | 00:10:05 | We start with the basics.  |
      | 2 | 00:50:00 | 00:50:05 | Then we go deeper.         |
      | 3 | 01:30:00 | 01:30:05 | Finally we wrap things up. |
    And the LLM provider "anthropic" answers "note_map" for chunk 1 with "note-map-part-1"
    And the LLM provider "anthropic" answers "note_map" for chunk 2 with "note-map-part-2"
    And the LLM provider "anthropic" answers "note_map" for chunk 3 with "note-map-part-3"
    And the LLM provider "anthropic" answers "note" with "note-reduce"
    When the owner reruns "yt:note02xxxxx" from "note"
    And the worker runs until idle
    Then the LLM provider "anthropic" received 3 requests for "note_map"
    And the LLM provider "anthropic" received 1 request for "note"
    And the note of "yt:note02xxxxx" contains "## Part three · [1:30:00](https://www.youtube.com/watch?v=note02xxxxx&t=5400s)"

  @NOTE-04 @NOTE-R7
  Scenario: A finished note passes every check
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    And the owner sends "GET /api/items/yt:note01xxxxx/note"
    Then the response contains:
      """
      { "checks": [
          { "id": "frontmatter", "ok": true },
          { "id": "heading_timestamps", "ok": true },
          { "id": "images_exist", "ok": true },
          { "id": "image_names", "ok": true },
          { "id": "self_check", "ok": true }
      ] }
      """

  @NOTE-04 @NOTE-R7
  Scenario: A note that fails a check is held back from the vault
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner sends "POST /api/note-templates" with:
      """
      { "id": "extra-heading", "body": "{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n\n## 補充說明\n\n{{.SelfCheck}}\n" }
      """
    Then the response status is 201
    When the owner sends "POST /api/items/yt:note01xxxxx/rerun" with:
      """
      { "from": "note", "overrides": { "stages": { "note": { "template": "extra-heading" } } } }
      """
    And the worker runs until idle
    Then the stage "note" of "yt:note01xxxxx" is blocked with the reason "needs_action"
    And "yt:note01xxxxx" shows the message "筆記沒有通過自動檢查：標題沒有時間戳（補充說明）。"
    And "yt:note01xxxxx" offers the actions "重新產生筆記, 開啟筆記模板"
    When the owner sends "GET /api/items/yt:note01xxxxx/note"
    Then the response contains:
      """
      { "checks": [ {}, { "id": "heading_timestamps", "ok": false, "message": "標題沒有時間戳（補充說明）" } ] }
      """

  @NOTE-01 @NOTE-R8
  Scenario: Untrusted markup in the model output is neutralised
    Given the LLM provider "anthropic" answers "note" with "note-hostile"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the note of "yt:note01xxxxx" does not contain "<script>"
    And the note of "yt:note01xxxxx" contains "&lt;script&gt;"
    And the note of "yt:note01xxxxx" does not contain "javascript:"
    And the note of "yt:note01xxxxx" does not contain "![[../../Private/diary.md]]"
    And the note of "yt:note01xxxxx" does not contain "```dataviewjs"
    And the note of "yt:note01xxxxx" does not contain "<%"
    And the note of "yt:note01xxxxx" contains "**Injected heading**"
    And the note of "yt:note01xxxxx" contains "[the docs](https://example.com/docs)"

  @NOTE-01 @NOTE-R9
  Scenario: Selected frames are embedded with their time and caption
    Given the LLM provider "anthropic" answers "note" with "note-with-frames"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the note of "yt:note01xxxxx" contains "![[Writing a Toy Kernel - 00-20 - frame_0001.jpg]]"
    And the note of "yt:note01xxxxx" contains "*00:20 — Toolchain diagram*"
    And the note of "yt:note01xxxxx" contains "![[Writing a Toy Kernel - 05-00 - frame_0003.jpg]]"

  @NOTE-03 @NOTE-R10
  Scenario: Importing fixed subtitles marks the note as outdated
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    And the file "/tmp/fixed.srt" contains:
      """
      1
      00:00:05,000 --> 00:00:10,000
      Welcome back to my channel.
      """
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    And the owner sends "POST /api/items/yt:note01xxxxx/subtitles/import" with:
      """
      { "path": "/tmp/fixed.srt", "lang": "en" }
      """
    And the owner sends "GET /api/items/yt:note01xxxxx/note"
    Then the response contains:
      """
      { "stale": true, "stale_reason": "字幕已更新，筆記可能過時。" }
      """

  @NOTE-05 @NOTE-R11
  Scenario: The built-in template can be copied but not changed
    When the owner sends "PATCH /api/note-templates/video-notes" with:
      """
      { "body": "# {{.Title}}" }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "builtin_template" }
      """
    When the owner sends "POST /api/note-templates" with:
      """
      { "id": "my-notes", "from": "video-notes" }
      """
    Then the response status is 201
    And the response contains:
      """
      { "id": "my-notes", "version": 1 }
      """
    When the owner sends "PATCH /api/note-templates/my-notes" with:
      """
      { "body": "{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n\n{{.SelfCheck}}\n" }
      """
    Then the response contains:
      """
      { "id": "my-notes", "version": 2 }
      """

  @NOTE-05 @NOTE-R11
  Scenario: Unknown template variables are rejected when saving
    When the owner sends "POST /api/note-templates" with:
      """
      { "id": "leaky", "body": "{{.Frontmatter}}\n{{.Secret}}\n" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "body", "message": "不認識的變數 .Secret。" } ] }
      """

  @NOTE-05 @NOTE-R11
  Scenario: Only admin tokens can change templates
    Given a token "reader" with scopes "read"
    When the client sends "POST /api/note-templates" with the token "reader" and:
      """
      { "id": "my-notes", "from": "video-notes" }
      """
    Then the response status is 403

  @NOTE-05 @NOTE-R12
  Scenario: A template preview reuses the current note without calling the model
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    And the owner sends "POST /api/note-templates/video-notes/preview" with:
      """
      { "item": "yt:note01xxxxx" }
      """
    Then the response status is 200
    And the response contains:
      """
      { "markdown": "*", "checks": [ { "id": "frontmatter", "ok": true } ] }
      """
    And the LLM provider "anthropic" received 1 request for "note"
    And the "note" artifact of "yt:note01xxxxx" has 1 version

  @NOTE-05 @NOTE-R12
  Scenario: Previewing on an item without a note says so
    Given a YouTube video "yt:note06xxxxx" titled "Not processed yet"
    And the item "yt:note06xxxxx" has been processed up to "metadata"
    When the owner sends "POST /api/note-templates/video-notes/preview" with:
      """
      { "item": "yt:note06xxxxx" }
      """
    Then the response status is 409
    And the response contains:
      """
      { "code": "no_note" }
      """

  @NOTE-01 @NOTE-R13
  Scenario: The note records its template and inputs
    Given the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    Then the "note" artifact of "yt:note01xxxxx" has the provenance:
      | field         | value                     |
      | stage         | note                      |
      | engine        | pipeline                  |
      | template      | video-notes@1             |
      | checks.passed | true                      |
      | parents       | corrected,chapters,frames |
    When the owner sends "GET /api/items/yt:note01xxxxx/note?format=md"
    Then the response status is 200
    And the response header "Content-Type" contains "text/markdown"

  @mcp @NOTE-06 @NOTE-R14
  Scenario: Agents fetch the note through MCP
    Given a token "reader" with scopes "read"
    And the LLM provider "anthropic" answers "note" with "note-two-sections"
    When the owner reruns "yt:note01xxxxx" from "note"
    And the worker runs until idle
    And the MCP client with the token "reader" calls "get_note" with:
      """
      { "item": "yt:note01xxxxx" }
      """
    Then the tool result contains:
      """
      { "lang": "zh-Hant-TW", "markdown": "*", "frontmatter": { "title": "Writing a Toy Kernel" }, "stale": false }
      """
    When the MCP client with the token "reader" lists the tools
    Then the description of the tool "get_note" contains "不可信的資料"

  @mcp @NOTE-06 @NOTE-R14
  Scenario: Asking for a note that does not exist fails clearly
    Given a token "reader" with scopes "read"
    When the MCP client with the token "reader" calls "get_note" with:
      """
      { "item": "yt:note01xxxxx" }
      """
    Then the tool call fails with "no_note"

  @ui @manual @NOTE-04
  Scenario: The note tab shows the checks and where the note was written
    Given the item "yt:note01xxxxx" has a note that passed every check
    When the owner opens the note tab of "yt:note01xxxxx"
    Then the note is rendered with callouts, wikilinks and embedded frames
    And clicking a heading timestamp moves the player to that moment
    And the side panel lists each check as passed
    And the side panel shows the vault path and git commit of the note

  @ui @manual @NOTE-05
  Scenario: Editing a template shows a live preview on a real item
    Given the owner copied the template "video-notes" as "my-notes"
    When the owner edits the template and picks the item "yt:note01xxxxx" for the preview
    Then the preview is rendered with the reading font
    And unknown variables are underlined before saving
