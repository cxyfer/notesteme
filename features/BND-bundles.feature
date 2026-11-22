@M4
Feature: Bundle export and import
  As the owner, I want to export an item with its transcripts, notes and provenance
  as a bundle, and have bundles imported safely, so that I can move items between
  instances without a malicious bundle harming mine.

  Background:
    Given the owner is signed in

  @BND-01 @BND-R1
  Scenario: The CLI exports the current transcripts and the note
    Given the bundle instance ID is "0190a1b2-0000-7000-8000-00000000beef"
    And a YouTube video "yt:bndexp1xxxx" titled "Exported Talk"
    And the "corrected" transcript of "yt:bndexp1xxxx" is:
      | # | start    | end      | text              |
      | 1 | 00:00:01 | 00:00:03 | Exported cue one. |
      | 2 | 00:00:03 | 00:00:06 | Exported cue two. |
    And the "translated" transcript of "yt:bndexp1xxxx" in "zh-Hant" is:
      | # | start    | end      | text       |
      | 1 | 00:00:01 | 00:00:03 | 匯出的第一句。 |
      | 2 | 00:00:03 | 00:00:06 | 匯出的第二句。 |
    And the note of "yt:bndexp1xxxx" is:
      """
      ---
      title: Exported Talk
      ---
      # Exported Talk

      A note that travels with the bundle.
      """
    When the owner runs "notesteme export yt:bndexp1xxxx --format bundle --out /tmp/exp.notesteme.zip"
    Then the command exits with 0
    And the bundle "/tmp/exp.notesteme.zip" passes its own checks
    And the bundle "/tmp/exp.notesteme.zip" lists the artifacts:
      | kind       | variant    | lang    |
      | transcript | corrected  | en      |
      | transcript | translated | zh-Hant |
      | note       |            | *       |
    And the bundle "/tmp/exp.notesteme.zip" has the manifest values:
      | field                  | value                                |
      | bundle_version         | 0                                    |
      | item.platform          | youtube                              |
      | item.external_id       | bndexp1xxxx                          |
      | item.title             | Exported Talk                        |
      | created_by.instance_id | 0190a1b2-0000-7000-8000-00000000beef |

  @BND-01 @BND-R1
  Scenario: The API serves the bundle as a download with the read scope
    Given the bundle instance ID is "0190a1b2-0000-7000-8000-00000000beef"
    And a YouTube video "yt:bndexp1xxxx" titled "Exported Talk"
    And the "corrected" transcript of "yt:bndexp1xxxx" is:
      | # | start    | end      | text              |
      | 1 | 00:00:01 | 00:00:03 | Exported cue one. |
    And a token "reader" with scopes "read"
    When the client sends "GET /api/items/yt:bndexp1xxxx/bundle" with the token "reader"
    Then the response status is 200
    And the response header "Content-Type" contains "application/zip"
    And the response header "Content-Disposition" contains "yt-bndexp1xxxx.notesteme.zip"

  @BND-01 @BND-R2
  Scenario: An exported bundle carries no secrets and no local paths
    Given the bundle instance ID is "0190a1b2-0000-7000-8000-00000000beef"
    And the environment variable "NOTESTEME_SUPADATA_API_KEY" is "sk-bundle-secret-0042"
    And a local file "lecture.mp4" in the inbox
    And the item "local:lecture.mp4" has been ingested
    And the "normalized" transcript of "local:lecture.mp4" is:
      | # | start    | end      | text                |
      | 1 | 00:00:01 | 00:00:03 | Welcome to lecture. |
    When the owner runs "notesteme export local:lecture.mp4 --format bundle --out /tmp/lecture.notesteme.zip"
    Then the command exits with 0
    And the bundle "/tmp/lecture.notesteme.zip" contains no local paths
    And the bundle "/tmp/lecture.notesteme.zip" does not contain "sk-bundle-secret-0042"
    And the bundle "/tmp/lecture.notesteme.zip" has the manifest values:
      | field            | value       |
      | item.platform    | local       |
      | item.source_name | lecture.mp4 |

  @BND-01 @BND-02 @BND-R3 @BND-R8
  Scenario: A valid bundle becomes an item with marked provenance and nothing runs
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And a YouTube video "yt:bndl01xxxxx" titled "Bundled Talk"
    And "yt:bndl01xxxxx" has manual captions in "en"
    When the bundle "/tmp/in.notesteme.zip" is imported
    Then the response status is 201
    And the response contains:
      """
      { "item": { "ref": "yt:bndl01xxxxx", "title": "Bundled Talk" }, "imported": 2, "skipped": 0 }
      """
    And the "corrected" transcript of "yt:bndl01xxxxx" is:
      | # | start    | end      | text             |
      | 1 | 00:00:01 | 00:00:03 | Bundled cue one. |
      | 2 | 00:00:03 | 00:00:06 | Bundled cue two. |
    And the "corrected" artifact of "yt:bndl01xxxxx" has the provenance:
      | field                     | value                                |
      | provider                  | anthropic                            |
      | imported_from.instance_id | 0190a1b2-0000-7000-8000-000000000001 |
      | imported_from.app_version | 0.4.0                                |
      | verified                  | false                                |
    And the stage "correct" of "yt:bndl01xxxxx" is "done"
    When the worker runs until idle
    Then the source "youtube-manual" was called 0 times

  @BND-02 @BND-R3 @cli
  Scenario: The CLI imports a bundle
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    When the owner runs "notesteme import /tmp/in.notesteme.zip --json"
    Then the command exits with 0
    And the command output is JSON containing:
      """
      { "item": { "ref": "yt:bndl01xxxxx" }, "imported": 2 }
      """

  @BND-02 @BND-R3
  Scenario: Importing needs the admin scope
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And a token "adder" with scopes "read,ingest"
    When the bundle "/tmp/in.notesteme.zip" is imported with the token "adder"
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """

  @BND-02 @BND-R3
  Scenario: An upload over the size limit is refused before it is read
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/in.notesteme.zip" also has the entries:
      | path              | type | size   | fill   |
      | artifacts/pad.bin | file | 1536KiB | random |
    And the config file contains:
      """
      bundles:
        import:
          max_upload_bytes: 1MiB
      """
    When the bundle "/tmp/in.notesteme.zip" is imported
    Then the response status is 413
    And the response contains:
      """
      { "code": "bundle_too_large", "message": "bundle 超過 1 MiB 的上傳上限。" }
      """
    And the library contains 0 items

  @BND-02 @BND-R4 @BND-R10
  Scenario Outline: Entries that could escape or alias are refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path   | type   | target   |
      | <path> | <type> | <target> |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "entry": "<path>", "message": "<message>" }
      """
    And the library contains 0 items

    Examples:
      | path                      | type        | target        | message                                                                                        |
      | /etc/passwd               | file        |               | bundle 中的項目「/etc/passwd」使用絕對路徑，已拒絕匯入。                                       |
      | C:/evil.json              | file        |               | bundle 中的項目「C:/evil.json」使用絕對路徑，已拒絕匯入。                                      |
      | ../evil.json              | file        |               | bundle 中的項目「../evil.json」含有「..」，已拒絕匯入。                                        |
      | artifacts/../../evil.json | file        |               | bundle 中的項目「artifacts/../../evil.json」含有「..」，已拒絕匯入。                           |
      | artifacts/sub/deep.json   | file        |               | bundle 中的項目「artifacts/sub/deep.json」不是允許的路徑（只能是 manifest.json 或 artifacts/<檔名>），已拒絕匯入。 |
      | notes/evil.md             | file        |               | bundle 中的項目「notes/evil.md」不是允許的路徑（只能是 manifest.json 或 artifacts/<檔名>），已拒絕匯入。 |
      | artifacts/link.json       | symlink     | /etc/passwd   | bundle 中的項目「artifacts/link.json」不是一般檔案（symlink），已拒絕匯入。                     |
      | artifacts/dev.json        | char-device |               | bundle 中的項目「artifacts/dev.json」不是一般檔案（裝置檔），已拒絕匯入。                       |
      | artifacts/pipe.json       | fifo        |               | bundle 中的項目「artifacts/pipe.json」不是一般檔案（FIFO），已拒絕匯入。                        |
      | artifacts/copy.json       | hardlink    | manifest.json | bundle 中的項目「artifacts/copy.json」與「manifest.json」共用同一份資料，已拒絕匯入。           |

  @BND-02 @BND-R4 @BND-R10
  Scenario: Backslashes in entry names are refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path                   | type |
      | artifacts\\..\\evil.json | file |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "entry": "artifacts\\..\\evil.json", "message": "bundle 中的項目「artifacts\\..\\evil.json」不是允許的路徑（只能是 manifest.json 或 artifacts/<檔名>），已拒絕匯入。" }
      """

  @BND-02 @BND-R4 @BND-R10
  Scenario: Entries that differ only in case are refused as duplicates
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path               | type |
      | artifacts/Dup.json | file |
      | artifacts/dup.json | file |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "bundle 中有重複的項目「artifacts/dup.json」，已拒絕匯入。" }
      """

  @BND-02 @BND-R4 @BND-R10
  Scenario: Encrypted entries are refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path                 | type           |
      | artifacts/secret.bin | encrypted-file |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "bundle 中的項目「artifacts/secret.bin」使用不支援的壓縮方式或加密，已拒絕匯入。" }
      """

  @BND-02 @BND-R5 @BND-R10
  Scenario: Too many entries are refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path            | type |
      | artifacts/1.bin | file |
      | artifacts/2.bin | file |
      | artifacts/3.bin | file |
    And the config file contains:
      """
      bundles:
        import:
          max_files: 5
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "bundle 的項目超過 5 個的上限，已拒絕匯入。" }
      """
    And the library contains 0 items

  @BND-02 @BND-R5 @BND-R10
  Scenario: A single file over the limit is refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path              | type | size   | fill   |
      | artifacts/big.bin | file | 300KiB | random |
    And the config file contains:
      """
      bundles:
        import:
          max_file_bytes: 256KiB
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "entry": "artifacts/big.bin", "message": "bundle 中的檔案「artifacts/big.bin」解壓後超過 256 KiB 的上限，已拒絕匯入。" }
      """

  @BND-02 @BND-R5 @BND-R10
  Scenario: A file that unpacks to more than its declared size is refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path              | type | size   | fill   | declared_size |
      | artifacts/big.bin | file | 300KiB | random | 10B           |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "entry": "artifacts/big.bin", "message": "bundle 中的檔案「artifacts/big.bin」實際大小與宣告的不符，已拒絕匯入。" }
      """
    And the library contains 0 items

  @BND-02 @BND-R5 @BND-R10
  Scenario: The total unpacked size is limited
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path            | type | size   | fill   |
      | artifacts/a.bin | file | 400KiB | random |
      | artifacts/b.bin | file | 400KiB | random |
      | artifacts/c.bin | file | 400KiB | random |
    And the config file contains:
      """
      bundles:
        import:
          max_total_bytes: 1MiB
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "bundle 解壓後合計超過 1 MiB 的上限，已拒絕匯入。" }
      """
    And the library contains 0 items

  @BND-02 @BND-R5 @BND-R10
  Scenario: A highly compressed file is refused as a zip bomb
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path                | type | size | fill  |
      | artifacts/zeros.bin | file | 2MiB | zeros |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "entry": "artifacts/zeros.bin", "message": "bundle 中的檔案「artifacts/zeros.bin」壓縮比超過 100 倍的上限，已拒絕匯入。" }
      """

  @BND-02 @BND-R6 @BND-R10
  Scenario Outline: The manifest is checked against its schema
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" has the manifest field "<field>" set to:
      """
      <value>
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "errors": [ { "path": "<field>" } ] }
      """
    And the library contains 0 items

    Examples:
      | field              | value             |
      | artifacts.0.kind   | "script"          |
      | artifacts.0.id     | "not-a-uuid"      |
      | item.platform      | "file"            |
      | item.run           | "rm -rf /"        |
      | hooks              | ["curl evil"]     |

  @BND-02 @BND-R6 @BND-R10
  Scenario: Other bundle versions are refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" has the manifest field "bundle_version" set to:
      """
      1
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_unsupported_version", "message": "不支援的 bundle 版本：1。這個版本只能匯入 bundle_version 0。" }
      """

  @BND-02 @BND-R7 @BND-R10
  Scenario: A file whose content does not match its sha256 is refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" has the content of "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json" changed
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "bundle 中的檔案「artifacts/0190a1b2-0000-7000-8000-0000000000a1.json」與 manifest 記錄的 sha256 不符，已拒絕匯入。" }
      """
    And the library contains 0 items

  @BND-02 @BND-R7 @BND-R10
  Scenario: A file that the manifest does not list is refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path                | type | listed |
      | artifacts/extra.bin | file | no     |
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "bundle 中的檔案「artifacts/extra.bin」沒有列在 manifest 中，已拒絕匯入。" }
      """

  @BND-02 @BND-R7 @BND-R10
  Scenario: A file that the manifest lists but the zip lacks is refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" has the manifest field "files.2" set to:
      """
      { "path": "artifacts/missing.json", "sha256": "0000000000000000000000000000000000000000000000000000000000000000", "size": 2 }
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "manifest 列出的檔案「artifacts/missing.json」不在 bundle 中，已拒絕匯入。" }
      """

  @BND-02 @BND-R7 @BND-R10
  Scenario: A transcript that is not valid cues JSON is refused
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" has the file "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json" replaced with:
      """
      { "cues": "not a list" }
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "entry": "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json" }
      """
    And the library contains 0 items

  @BND-02 @BND-R10
  Scenario: A file that is not a zip is refused
    Given the bundle "/tmp/bad.notesteme.zip" is the text:
      """
      this is not a zip archive
      """
    When the bundle "/tmp/bad.notesteme.zip" is imported
    Then the response status is 422
    And the response contains:
      """
      { "code": "bundle_invalid", "message": "這不是 Notesteme bundle（無法以 zip 讀取）。" }
      """

  @BND-02 @BND-R10 @cli
  Scenario: The CLI reports a refused bundle with exit code 1
    Given the bundle "/tmp/bad.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And the bundle "/tmp/bad.notesteme.zip" also has the entries:
      | path         | type |
      | ../evil.json | file |
    When the owner runs "notesteme import /tmp/bad.notesteme.zip"
    Then the command exits with 1
    And the command output contains "bundle 中的項目「../evil.json」含有「..」，已拒絕匯入。"
    And the library contains 0 items

  @BND-02 @BND-R9
  Scenario: An item that already exists is not merged by default
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And a YouTube video "yt:bndl01xxxxx" titled "Bundled Talk"
    And the item "yt:bndl01xxxxx" has been processed up to "metadata"
    When the bundle "/tmp/in.notesteme.zip" is imported
    Then the response status is 409
    And the response contains:
      """
      { "code": "bundle_item_exists", "message": "這部影音已在媒體庫中（yt:bndl01xxxxx）。要把 bundle 的產出加成新版本，請加上 on_existing=add_versions。" }
      """
    And the item "yt:bndl01xxxxx" has no "corrected" artifact

  @BND-02 @BND-R9
  Scenario: Adding versions to an existing item skips identical content
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And a YouTube video "yt:bndl01xxxxx" titled "Bundled Talk"
    And the item "yt:bndl01xxxxx" has been processed up to "metadata"
    When the bundle "/tmp/in.notesteme.zip" is imported with "on_existing=add_versions"
    Then the response status is 201
    When the bundle "/tmp/in.notesteme.zip" is imported with "on_existing=add_versions"
    Then the response status is 201
    And the response contains:
      """
      { "imported": 0, "skipped": 2 }
      """
    And the "corrected" artifact of "yt:bndl01xxxxx" has 1 version

  @BND-02 @BND-R9
  Scenario: An item in the trash must be restored or purged first
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And a YouTube video "yt:bndl01xxxxx" titled "Bundled Talk"
    And the item "yt:bndl01xxxxx" has been processed up to "metadata"
    When the owner sends "DELETE /api/items/yt:bndl01xxxxx"
    And the bundle "/tmp/in.notesteme.zip" is imported
    Then the response status is 409
    And the response contains:
      """
      { "code": "bundle_item_in_trash", "message": "這部影音在垃圾桶中。請先還原或永久刪除後再匯入。" }
      """

  @BND-02 @BND-R9
  Scenario: A purged item can be imported again like a manual add
    Given the bundle "/tmp/in.notesteme.zip" contains the item "yt:bndl01xxxxx" titled "Bundled Talk"
    And a YouTube video "yt:bndl01xxxxx" titled "Bundled Talk"
    And the item "yt:bndl01xxxxx" has been processed up to "metadata"
    When the owner sends "DELETE /api/items/yt:bndl01xxxxx"
    And the owner sends "DELETE /api/trash/yt:bndl01xxxxx"
    And the bundle "/tmp/in.notesteme.zip" is imported
    Then the response status is 201
    And the library contains "yt:bndl01xxxxx"

  @BND-02 @BND-R7 @ui @manual
  Scenario: An imported note is shown without raw HTML
    Given a bundle whose note contains "<script>alert(1)</script>" and "<img src=x onerror=alert(1)>"
    When the owner imports it from the settings page and opens the note tab of the item
    Then the note shows the tags as plain text and no script runs
    And the item page says the item was imported from a bundle and shows the instance it came from
