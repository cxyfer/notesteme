@M3
Feature: Vault output and hooks
  As the owner, I want my notes written into my Obsidian vault folder or pushed
  to its git repository, never overwriting my edits, and my own command run
  afterwards.

  Background:
    Given the owner is signed in
    And the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
      hooks:
        notify: { argv: ["notesteme-test-hook", "{{note_path}}", "{{title}}"], timeout: 2s, env_allowlist: [LANG] }
      """
    And a YouTube video "yt:vlt001xxxxx" with:
      | field        | value                |
      | title        | Designing a Cache    |
      | channel      | Systems Weekly       |
      | published_at | 2025-08-09T10:00:00Z |
    And the profile "vault-test" is:
      """
      outputs:
        - { target: vault, vault: main, formats: [srt] }
      """
    And the default profile is "vault-test"
    And the note of "yt:vlt001xxxxx" is:
      """
      ---
      title: "Designing a Cache"
      notesteme_status: "complete"
      ---
      # Designing a Cache

      ## Why cache · [00:05](https://www.youtube.com/watch?v=vlt001xxxxx&t=5s)
      Caches trade memory for latency.

      ## 自我檢驗
      - Why does a cache help?
      """
    And the "corrected" transcript of "yt:vlt001xxxxx" is:
      | # | start    | end      | text                             |
      | 1 | 00:00:05 | 00:00:08 | Caches trade memory for latency. |

  @VAULT-01 @VAULT-R1 @VAULT-R2 @VAULT-R5
  Scenario: The note and its subtitles are written into the vault folder
    When the owner reruns "yt:vlt001xxxxx" from "publish"
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "done"
    And the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains "Caches trade memory for latency."
    And the file "/vault/Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt" contains "Caches trade memory for latency."
    When the owner sends "GET /api/items/yt:vlt001xxxxx/outputs"
    Then the response contains:
      """
      { "outputs": [
          { "target": "vault", "vault": "main", "kind": "note", "path": "Video Notes/YouTube/Designing a Cache - 2025-08-09.md", "state": "written" }
      ] }
      """

  @VAULT-01 @VAULT-R1
  Scenario: Profiles can only pick vaults defined in the config file
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "rogue", "config": { "outputs": [ { "target": "vault", "vault": "elsewhere" } ] } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "outputs.0.vault", "message": "沒有這個 vault：elsewhere。vault 只能在設定檔定義。" } ] }
      """
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "rogue-path", "config": { "outputs": [ { "target": "vault", "vault": "main", "path": "/etc" } ] } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "outputs.0.path", "message": "不認識的欄位。" } ] }
      """

  @VAULT-01 @VAULT-R2
  Scenario: Channel folders use the date-first layout
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "formats": ["srt"], "folder": "Channel/{channel}" } ] } }
      """
    And the worker runs until idle
    Then the file "/vault/Video Notes/Channel/Systems Weekly/2025-08-09_Designing a Cache.md" exists
    And the file "/vault/Video Notes/Channel/Systems Weekly/_Subtitles/2025-08-09_Designing a Cache.srt" exists

  @VAULT-01 @VAULT-R2
  Scenario: The note links its subtitles by the full vault path
    Given the profile "vault-note" is:
      """
      stages:
        note: { executor: api, provider: anthropic, model: fixture-model, prompt: note-default, template: video-notes }
      outputs:
        - { target: vault, vault: main, formats: [srt] }
      """
    And the default profile is "vault-note"
    And the LLM provider "anthropic" answers "note" with "note-minimal"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "note", "profile": "vault-note" }
      """
    And the worker runs until idle
    Then the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains "[[Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt]]"

  @VAULT-01 @VAULT-R2 @VAULT-R4
  Scenario: Folders cannot leave the vault
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "folder": "../outside" } ] } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "outputs.0.folder", "message": "folder 不能是絕對路徑或包含 ..。" } ] }
      """

  @VAULT-01 @VAULT-R3
  Scenario Outline: Titles become safe file names
    Given a YouTube video "<ref>" with:
      | field        | value                |
      | title        | <title>              |
      | published_at | 2025-08-09T10:00:00Z |
    And the note of "<ref>" is:
      """
      ---
      notesteme_status: "complete"
      ---
      ## Overview · [00:05](https://www.youtube.com/watch?v=x&t=5s)

      ## 自我檢驗
      - Why?
      """
    When the owner reruns "<ref>" from "publish"
    And the worker runs until idle
    Then the file "/vault/Video Notes/YouTube/<file>" exists

    Examples:
      | ref            | title                         | file                                   |
      | yt:vlt002xxxxx | Cache: what? <fast> \| "slow" | Cache what fast slow - 2025-08-09.md   |
      | yt:vlt003xxxxx | CON                           | _CON - 2025-08-09.md                   |
      | yt:vlt004xxxxx | ../../etc/passwd              | ....etcpasswd - 2025-08-09.md          |

  @VAULT-01 @VAULT-R5
  Scenario: Frames used by the note are written next to it
    Given the frames of "yt:vlt001xxxxx" are:
      | # | at       | selected | caption        |
      | 1 | 00:00:05 | yes      | Cache diagram  |
    And the note of "yt:vlt001xxxxx" is:
      """
      ---
      notesteme_status: "complete"
      ---
      ## Why cache · [00:05](https://www.youtube.com/watch?v=vlt001xxxxx&t=5s)
      ![[Designing a Cache - 00-05 - frame_0001.jpg]]
      *00:05 — Cache diagram*

      ## 自我檢驗
      - Why does a cache help?
      """
    When the owner reruns "yt:vlt001xxxxx" from "publish"
    And the worker runs until idle
    Then the file "/vault/Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache - 00-05 - frame_0001.jpg" exists

  @VAULT-03 @VAULT-R6
  Scenario: A note edited in the vault is kept and the new one is saved beside it
    Given the clock is at "2026-11-23T08:00:00Z"
    When the owner reruns "yt:vlt001xxxxx" from "publish"
    And the worker runs until idle
    Given the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains:
      """
      My own notes about caches.
      """
    When the owner reruns "yt:vlt001xxxxx" from "publish"
    And the worker runs until idle
    Then the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains "My own notes about caches."
    And the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09 (notesteme 2026-11-23).md" contains "Caches trade memory for latency."

  @VAULT-03 @VAULT-R6 @VAULT-R11
  Scenario: With the skip policy an edited note blocks publishing and the hook
    When the owner reruns "yt:vlt001xxxxx" from "publish"
    And the worker runs until idle
    Given the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains:
      """
      My own notes about caches.
      """
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "formats": ["srt"], "on_user_change": "skip", "hooks": ["notify"] } ] } }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is blocked with the reason "needs_action"
    And "yt:vlt001xxxxx" offers the actions "處理該檔案後重新發布"
    And the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains "My own notes about caches."
    And the hook "notify" did not run

  @VAULT-02 @VAULT-R7 @VAULT-R8
  Scenario: Only the files of this publish are committed and pushed
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the environment variable "NOTESTEME_VAULT_REMOTE_TOKEN" is "vault-token-123"
    And a YouTube video "yt:vlt005xxxxx" with:
      | field        | value                |
      | title        | Second Talk          |
      | published_at | 2025-08-10T10:00:00Z |
    And the note of "yt:vlt005xxxxx" is:
      """
      ---
      notesteme_status: "complete"
      ---
      ## Overview · [00:05](https://www.youtube.com/watch?v=vlt005xxxxx&t=5s)

      ## 自我檢驗
      - Why?
      """
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "remote", "formats": ["srt"] } ] } }
      """
    And the worker runs until idle
    Given the file "/data/vault-repo/remote/Inbox/draft.md" contains:
      """
      An unfinished draft that is not Notesteme's.
      """
    When the owner sends "POST /api/items/yt:vlt005xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "remote", "formats": [] } ] } }
      """
    And the worker runs until idle
    Then the vault remote received a commit with the files:
      | path                                            |
      | Video Notes/YouTube/Second Talk - 2025-08-10.md |
    And every git command for the vault ran with "core.hooksPath=/dev/null"
    And every git command for the vault ran with "credential.helper="
    And no git command for the vault contained "vault-token-123"
    And the logs do not contain "vault-token-123"

  @VAULT-02 @VAULT-R7
  Scenario: Publishing an unchanged note pushes nothing new
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    And the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Then the vault remote received a single new commit

  # push 是網路操作：有 git vault 時 publish 用 download 資源類別，沒有時是 local（ADR-0020 第 3 點、VAULT 介面）。
  @VAULT-02 @VAULT-R7
  Scenario: Publishing to a git vault waits for a download slot
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    And the resource class "download" is paused
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "pending"
    When the owner sends "GET /api/queue"
    Then the response contains:
      """
      { "tasks": [ { "item": "yt:vlt001xxxxx", "stage": "publish", "resource_class": "download", "state": "pending" } ] }
      """
    When the owner sends "POST /api/queue/resume" with:
      """
      { "class": "download" }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "done"
    And the vault remote received a single new commit

  @VAULT-02 @VAULT-R7
  Scenario: Publishing to a folder vault does not wait for downloads
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the resource class "download" is paused
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-test" }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "done"

  @VAULT-02 @VAULT-R8
  Scenario Outline: Remotes that could run commands are refused at startup
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "<remote>", branch: master }
      """
    When the server starts
    Then the server fails to start with "vaults.remote.remote 只接受 https 或 ssh 網址。"

    Examples:
      | remote                         |
      | ext::sh -c touch% /tmp/pwned   |
      | file:///data/config            |
      | /srv/git/vault.git             |

  @VAULT-02 @VAULT-R9
  Scenario: A rejected push asks the owner to sync the vault
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    And the vault remote rejects pushes as "non-fast-forward"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is blocked with the reason "needs_action"
    And "yt:vlt001xxxxx" shows the message "vault 的遠端分支 master 有本機沒有的提交，Notesteme 不會自動合併。請先同步 vault repo 再重新發布。"
    And "yt:vlt001xxxxx" offers the actions "重新發布"

  @VAULT-02 @VAULT-R9
  Scenario: Bad credentials ask for new ones
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    And the vault remote rejects pushes as "bad credentials"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is blocked with the reason "needs_action"
    And "yt:vlt001xxxxx" shows the message "無法登入 vault 的遠端（認證失敗）。請更新 vault remote 的憑證。"
    And "yt:vlt001xxxxx" offers the actions "更新憑證, 重新發布"

  @VAULT-02 @VAULT-R9
  Scenario: A network error is retried without a second commit
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    And the vault remote fails the next push with a network error
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "pending"
    When the clock advances by "1m"
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "done"
    And the vault remote received a single new commit

  @VAULT-04 @VAULT-R10
  Scenario: Hooks can only be picked by id
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "rogue-hook", "config": { "outputs": [ { "target": "vault", "vault": "main", "hooks": ["missing"] } ] } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "outputs.0.hooks.0", "message": "沒有這個 hook：missing。hook 只能在設定檔定義。" } ] }
      """
    When the owner sends "POST /api/profiles" with:
      """
      { "name": "inline-hook", "config": { "outputs": [ { "target": "vault", "vault": "main", "hooks": [ { "argv": ["sh", "-c", "id"] } ] } ] } }
      """
    Then the response status is 422
    And the response contains:
      """
      { "errors": [ { "path": "outputs.0.hooks.0", "message": "必須是字串。" } ] }
      """

  @VAULT-04 @VAULT-R10 @VAULT-R11
  Scenario: A hook gets its arguments as an argv array without a shell
    Given a YouTube video "yt:vlt006xxxxx" with:
      | field        | value                          |
      | title        | $(touch /tmp/pwned); rm -rf ~  |
      | published_at | 2025-08-09T10:00:00Z           |
    And the note of "yt:vlt006xxxxx" is:
      """
      ---
      notesteme_status: "complete"
      ---
      ## Overview · [00:05](https://www.youtube.com/watch?v=vlt006xxxxx&t=5s)

      ## 自我檢驗
      - Why?
      """
    When the owner sends "POST /api/items/yt:vlt006xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "formats": [], "hooks": ["notify"] } ] } }
      """
    And the worker runs until idle
    Then the hook "notify" ran with the arguments:
      """
      ["/vault/Video Notes/YouTube/$(touch tmppwned); rm -rf ~ - 2025-08-09.md", "$(touch /tmp/pwned); rm -rf ~"]
      """

  @VAULT-04 @VAULT-R11
  Scenario: A hook only sees the environment variables it is allowed
    Given the environment variable "LANG" is "C.UTF-8"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "hooks": ["notify"] } ] } }
      """
    And the worker runs until idle
    Then the hook "notify" saw the environment variable "LANG"
    And the hook "notify" saw the environment variable "NOTESTEME_ITEM_ID"
    And the hook "notify" did not see the environment variable "NOTESTEME_MASTER_KEY"
    And the hook "notify" did not see the environment variable "NOTESTEME_PASSWORD"

  @VAULT-04 @VAULT-R11
  Scenario: Secrets cannot be passed to hooks
    Given the config file contains:
      """
      hooks:
        leak: { argv: ["notesteme-test-hook"], env_allowlist: [NOTESTEME_MASTER_KEY] }
      """
    When the server starts
    Then the server fails to start with "hooks.leak.env_allowlist 不能包含 NOTESTEME_MASTER_KEY。"

  @VAULT-04 @VAULT-R11
  Scenario Outline: A failing or slow hook leaves the written files in place
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
      hooks:
        flaky: { argv: ["notesteme-test-hook", "<flag>", "<value>"], timeout: 1s, max_output: 1KiB }
      """
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "hooks": ["flaky"] } ] } }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "done"
    And the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" exists
    And "yt:vlt001xxxxx" shows the message "<message>"
    And "yt:vlt001xxxxx" offers the actions "重新發布"
    And no task is running

    Examples:
      | flag   | value | message                                         |
      | --exit | 3     | hook flaky 失敗（結束碼 3）。檔案已經寫入。     |
      | --hang | 0     | hook flaky 逾時（1 秒），已停止。檔案已經寫入。 |

  @VAULT-04 @VAULT-R11
  Scenario: Only the start of a long hook output is kept in the log
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
      hooks:
        chatty: { argv: ["notesteme-test-hook", "--print-bytes", "65536"], max_output: 1KiB }
      """
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "overrides": { "outputs": [ { "target": "vault", "vault": "main", "hooks": ["chatty"] } ] } }
      """
    And the worker runs until idle
    Then the stage "publish" of "yt:vlt001xxxxx" is "done"
    And the logs do not contain "END-OF-HOOK-OUTPUT"

  @VAULT-01 @VAULT-R12
  Scenario: Deleting an item can remove its untouched vault files
    When the owner reruns "yt:vlt001xxxxx" from "publish"
    And the worker runs until idle
    And the owner sends "DELETE /api/items/yt:vlt001xxxxx" with:
      """
      { "delete_outputs": true }
      """
    Then the response status is 200
    And the file "/vault/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" does not exist

  @VAULT-01 @VAULT-R12
  Scenario: Deleting an item removes its untouched files from the git vault in one commit
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Given the file "/data/vault-repo/remote/Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt" contains:
      """
      My own subtitle edits.
      """
    When the owner sends "DELETE /api/items/yt:vlt001xxxxx" with:
      """
      { "delete_outputs": true }
      """
    Then the response status is 200
    And the response contains:
      """
      { "kept_outputs": [ { "path": "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt", "reason": "modified" } ] }
      """
    And the vault remote received a commit with the files:
      | path                                                  |
      | Video Notes/YouTube/Designing a Cache - 2025-08-09.md |
    And the file "/data/vault-repo/remote/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" does not exist

  @VAULT-01 @VAULT-R12 @VAULT-R9
  Scenario: A git vault that cannot be synced keeps the files of a deleted item
    Given the config file contains:
      """
      vaults:
        main: { adapter: fs, path: /vault }
        remote: { adapter: git, remote: "https://git.example.com/me/vault.git", branch: master }
      """
    And the profile "vault-git" is:
      """
      outputs:
        - { target: vault, vault: remote, formats: [srt] }
      """
    And the default profile is "vault-git"
    When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:
      """
      { "from": "publish", "profile": "vault-git" }
      """
    And the worker runs until idle
    Given the vault remote rejects pushes as "non-fast-forward"
    When the owner sends "DELETE /api/items/yt:vlt001xxxxx" with:
      """
      { "delete_outputs": true }
      """
    Then the response status is 200
    And the response contains:
      """
      { "kept_outputs": [
          { "path": "Video Notes/YouTube/Designing a Cache - 2025-08-09.md", "reason": "git_rejected" },
          { "path": "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt", "reason": "git_rejected" }
      ] }
      """
    And the file "/data/vault-repo/remote/Video Notes/YouTube/Designing a Cache - 2025-08-09.md" contains "Caches trade memory for latency."

  @ui @manual @VAULT-01
  Scenario: The output settings show the vaults and hooks from the config file
    Given the config file defines the vault "main" and the hook "notify"
    When the owner opens the output section of the settings
    Then the vault "main" is listed with its adapter and base folder, without a way to edit its path
    And the hook "notify" can be picked but not edited
