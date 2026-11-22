@M4
Feature: Term candidate review
  As the owner, I want to review the term candidates collected during correction,
  so that my glossary grows without manual typing.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:glos01xxxxx" with:
      | field      | value                    |
      | title      | Container Basics         |
      | channel    | Channel A                |
      | channel_id | UCglosaaaaaaaaaaaaaaaaaa |
    And a YouTube video "yt:glos02xxxxx" with:
      | field      | value                    |
      | title      | Cluster Operations       |
      | channel    | Channel B                |
      | channel_id | UCglosbbbbbbbbbbbbbbbbbb |
    And the term candidates "Kubernetees, Helm Chartz" are pending for the channel of "yt:glos01xxxxx"
    And the term candidates "Istiio" are pending for the channel of "yt:glos02xxxxx"
    And the profile "glos-correct" is:
      """
      stages:
        correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default, min_similarity: 0.6 }
      """
    And the "segmented" transcript of "yt:glos01xxxxx" is:
      | # | start    | end      | text                       |
      | 1 | 00:00:01 | 00:00:03 | we deploy with helm charts |
    And the "segmented" transcript of "yt:glos02xxxxx" is:
      | # | start    | end      | text                       |
      | 1 | 00:00:01 | 00:00:03 | we run cubernetes clusters |
    And the LLM provider "anthropic" answers "correct" with "correct-echo"

  @GLOS-01 @GLOS-R5
  Scenario: Pending candidates from every channel are listed oldest first
    When the owner sends "GET /api/term-candidates?state=pending"
    Then the response status is 200
    And the response contains:
      """
      { "total": 3,
        "candidates": [
          { "term": "Kubernetees", "state": "pending",
            "channel": { "key": "youtube:UCglosaaaaaaaaaaaaaaaaaa", "title": "Channel A" },
            "item": { "ref": "yt:glos01xxxxx", "title": "Container Basics" } },
          { "term": "Helm Chartz", "channel": { "title": "Channel A" } },
          { "term": "Istiio", "channel": { "title": "Channel B" }, "item": { "ref": "yt:glos02xxxxx" } } ] }
      """

  @GLOS-01 @GLOS-R5
  Scenario: The review list can be narrowed to one channel
    When the owner sends "GET /api/term-candidates?state=pending&channel=youtube:UCglosbbbbbbbbbbbbbbbbbb"
    Then the response contains:
      """
      { "total": 1, "candidates": [ { "term": "Istiio" } ] }
      """
    When the owner sends "GET /api/term-candidates?state=maybe"
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "errors": [ { "path": "state" } ] }
      """

  @GLOS-01 @GLOS-R5
  Scenario: Reading the review list needs only the read scope
    Given a token "reader" with scopes "read"
    When the client sends "GET /api/term-candidates" with the token "reader"
    Then the response status is 200
    When the client sends "POST /api/term-candidates/decide" with the token "reader"
    Then the response status is 403
    And the response contains:
      """
      { "required_scope": "admin" }
      """

  @GLOS-01 @GLOS-R1 @GLOS-R3 @GLOS-R6
  Scenario: A term fixed and accepted globally is used when another channel is corrected
    When the term candidate "Kubernetees" of the channel of "yt:glos01xxxxx" is decided with:
      """
      { "state": "accepted", "term": "Kubernetes", "scope": "global" }
      """
    Then the response status is 200
    And the response contains:
      """
      { "term": "Kubernetees", "state": "accepted", "decided_term": "Kubernetes", "decided_scope": "global", "decided_at": "*" }
      """
    And the glossary "global" has exactly the terms "Kubernetes"
    When the owner sends "POST /api/items/yt:glos02xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "glos-correct" }
      """
    And the worker runs until idle
    Then request 1 to "anthropic" contains "Kubernetes"
    And request 1 to "anthropic" does not contain "Kubernetees"

  @GLOS-01 @GLOS-R1 @GLOS-R3 @GLOS-R6
  Scenario: Without a scope the term goes to its own channel only, as in M2
    When the term candidate "Helm Chartz" of the channel of "yt:glos01xxxxx" is decided with:
      """
      { "state": "accepted" }
      """
    Then the response status is 200
    And the response contains:
      """
      { "state": "accepted", "decided_term": "Helm Chartz", "decided_scope": "channel" }
      """
    And the glossary "channel-of:yt:glos01xxxxx" has exactly the terms "Helm Chartz"
    And the glossary "global" has exactly the terms ""
    When the owner sends "POST /api/items/yt:glos02xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "glos-correct" }
      """
    And the worker runs until idle
    And the owner sends "POST /api/items/yt:glos01xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "glos-correct" }
      """
    And the worker runs until idle
    Then request 1 to "anthropic" does not contain "Helm Chartz"
    And request 2 to "anthropic" contains "Helm Chartz"

  @GLOS-01 @GLOS-R1 @GLOS-R6
  Scenario: A rejected term stays out of the glossary
    When the term candidate "Istiio" of the channel of "yt:glos02xxxxx" is decided with:
      """
      { "state": "rejected" }
      """
    Then the response status is 200
    And the glossary "channel-of:yt:glos02xxxxx" has exactly the terms ""
    When the owner sends "POST /api/items/yt:glos02xxxxx/rerun" with:
      """
      { "from": "correct", "profile": "glos-correct" }
      """
    And the worker runs until idle
    Then request 1 to "anthropic" does not contain "Istiio"

  @GLOS-01 @GLOS-R1
  Scenario: A rejection cannot carry a new term or scope
    When the term candidate "Istiio" of the channel of "yt:glos02xxxxx" is decided with:
      """
      { "state": "rejected", "term": "Istio" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "message": "拒絕時不能指定 term 或 scope。" }
      """

  @GLOS-01 @GLOS-R2
  Scenario: A fixed term is cleaned before it enters the glossary
    When the term candidate "Kubernetees" of the channel of "yt:glos01xxxxx" is decided with:
      """
      { "state": "accepted", "term": "  Apache\u0009Kafka   Streams​ " }
      """
    Then the response status is 200
    And the response contains:
      """
      { "decided_term": "Apache Kafka Streams" }
      """
    And the glossary "channel-of:yt:glos01xxxxx" has exactly the terms "Apache Kafka Streams"

  @GLOS-01 @GLOS-R2
  Scenario Outline: Unusable fixed terms are refused and the candidate stays pending
    When the term candidate "Kubernetees" of the channel of "yt:glos01xxxxx" is decided with:
      """
      { "state": "accepted", "term": "<term>" }
      """
    Then the response status is 422
    And the response contains:
      """
      { "code": "invalid_request", "message": "<message>", "errors": [ { "path": "term" } ] }
      """
    When the owner sends "GET /api/term-candidates?state=pending"
    Then the response contains:
      """
      { "total": 3 }
      """

    Examples:
      | term                                                                                                  | message                |
      | \u0007\u0007                                                                                          | 術語不能是空白。       |
      | kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk | 術語最多 100 個字元。  |

  @GLOS-01 @GLOS-R4
  Scenario: Many candidates are decided in one batch
    When the term candidates are decided in one batch:
      | item           | term        | state    | decided_term | scope   |
      | yt:glos01xxxxx | Kubernetees | accepted | Kubernetes   | global  |
      | yt:glos01xxxxx | Helm Chartz | rejected |              |         |
      | yt:glos02xxxxx | Istiio      | accepted | Istio        | channel |
    Then the response status is 200
    And the response contains:
      """
      { "candidates": [
          { "term": "Kubernetees", "decided_term": "Kubernetes" },
          { "term": "Helm Chartz", "state": "rejected" },
          { "term": "Istiio", "decided_term": "Istio" } ] }
      """
    And the glossary "global" has exactly the terms "Kubernetes"
    And the glossary "channel-of:yt:glos02xxxxx" has exactly the terms "Istio"
    When the owner sends "GET /api/term-candidates?state=pending"
    Then the response contains:
      """
      { "total": 0 }
      """

  @GLOS-01 @GLOS-R4
  Scenario: A batch with one bad decision changes nothing
    When the term candidate "Helm Chartz" of the channel of "yt:glos01xxxxx" is decided with:
      """
      { "state": "rejected" }
      """
    And the term candidates are decided in one batch:
      | item           | term        | state    | decided_term | scope  |
      | yt:glos01xxxxx | Kubernetees | accepted | Kubernetes   | global |
      | yt:glos01xxxxx | Helm Chartz | accepted |              |        |
    Then the response status is 409
    And the response contains:
      """
      { "code": "term_candidate_decided", "errors": [ { "path": "decisions.1" } ] }
      """
    And the glossary "global" has exactly the terms ""
    When the owner sends "GET /api/term-candidates?state=pending"
    Then the response contains:
      """
      { "total": 2 }
      """

  @GLOS-01 @GLOS-R7 @ui @manual
  Scenario: The owner reviews candidates in the glossary settings
    Given there are pending term candidates in two channels
    When the owner opens the overview
    Then "需要你處理" shows "有 3 個術語等你確認。" with a "去審核" button
    When the owner opens the "詞彙表" section of the settings and chooses "改寫後接受" for "Kubernetees"
    Then the input is prefilled with "Kubernetees" and the owner can pick "加到這個頻道" or "加到全域"
    When the owner selects the remaining two candidates and rejects them together
    Then the list is empty and both channels' glossaries show only the accepted term
