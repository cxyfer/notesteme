@M1
Feature: Normalize transcripts
  As the owner, I want every source cleaned into one consistent sequence of cues
  with forward-moving timings, independent of my target languages.

  Background:
    Given the owner is signed in
    And a YouTube video "yt:abc123" titled "Rolling captions"

  @NRM-01 @NRM-R1
  Scenario: Rolling auto captions are de-duplicated
    Given the "source" transcript of "yt:abc123" is:
      | # | start    | end      | text                        |
      | 1 | 00:00:01 | 00:00:03 | so today we                 |
      | 2 | 00:00:02 | 00:00:04 | so today we talk about      |
      | 3 | 00:00:03 | 00:00:06 | so today we talk about Kafka |
    When the owner reruns "yt:abc123" from "normalize"
    And the worker runs until idle
    Then the "normalized" transcript of "yt:abc123" is:
      | # | start    | end      | text                         |
      | 1 | 00:00:01 | 00:00:06 | so today we talk about Kafka |

  @NRM-01 @NRM-R2
  Scenario: A tiny fragment is merged into the previous cue
    Given the "source" transcript of "yt:abc123" is:
      | # | start        | end          | text            |
      | 1 | 00:00:01.000 | 00:00:03.000 | this is the end |
      | 2 | 00:00:03.000 | 00:00:03.200 | .               |
    When the owner reruns "yt:abc123" from "normalize"
    And the worker runs until idle
    Then the "normalized" transcript of "yt:abc123" is:
      | # | start        | end          | text             | timing |
      | 1 | 00:00:01.000 | 00:00:03.200 | this is the end. | merged |

  @NRM-01 @NRM-R3 @NRM-R5
  Scenario: Formatting tags and empty cues are removed but music markers stay
    Given the "source" transcript of "yt:abc123" is:
      | # | start    | end      | text                     |
      | 1 | 00:00:01 | 00:00:03 | {\an8}<i>Hello  there</i> |
      | 2 | 00:00:03 | 00:00:04 |                          |
      | 3 | 00:00:04 | 00:00:07 | [Music]                  |
    When the owner reruns "yt:abc123" from "normalize"
    And the worker runs until idle
    Then the "normalized" transcript of "yt:abc123" is:
      | # | text        |
      | 1 | Hello there |
      | 2 | [Music]     |

  @NRM-02 @NRM-R4
  Scenario: Overlapping cues are trimmed so timings always move forward
    Given the "source" transcript of "yt:abc123" is:
      | # | start        | end          | text         |
      | 1 | 00:00:01.000 | 00:00:04.000 | first line   |
      | 2 | 00:00:03.500 | 00:00:06.000 | second line  |
    When the owner reruns "yt:abc123" from "normalize"
    And the worker runs until idle
    Then the "normalized" transcript of "yt:abc123" is:
      | # | start        | end          | text        |
      | 1 | 00:00:01.000 | 00:00:03.460 | first line  |
      | 2 | 00:00:03.500 | 00:00:06.000 | second line |

  @NRM-02 @NRM-R4
  Scenario: Out-of-order cues are sorted by start time
    Given the "source" transcript of "yt:abc123" is:
      | # | start    | end      | text   |
      | 1 | 00:00:05 | 00:00:07 | later  |
      | 2 | 00:00:01 | 00:00:03 | sooner |
    When the owner reruns "yt:abc123" from "normalize"
    And the worker runs until idle
    Then the "normalized" transcript of "yt:abc123" is:
      | # | text   |
      | 1 | sooner |
      | 2 | later  |

  @NRM-01 @NRM-R6
  Scenario: Word timings survive a merge
    Given the "source" transcript of "yt:abc123" is:
      | # | start    | end      | text       | words                                   |
      | 1 | 00:00:01 | 00:00:02 | hello      | hello@00:00:01.000-00:00:01.800         |
      | 2 | 00:00:02 | 00:00:03 | hello world | hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.900 |
    When the owner reruns "yt:abc123" from "normalize"
    And the worker runs until idle
    Then the "normalized" transcript of "yt:abc123" is:
      | # | text        | words                                                            |
      | 1 | hello world | hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.900 |

  @NRM-03 @NRM-R7
  Scenario: Changing the target language does not re-run normalization
    Given "yt:abc123" has auto captions in "zh-Hans"
    And the item "yt:abc123" has been processed up to "normalize"
    When the owner sends "POST /api/items/yt:abc123/rerun" with:
      """
      { "from": "normalize", "overrides": { "language": { "targets": ["en"] } } }
      """
    And the worker runs until idle
    Then the "normalized" artifact of "yt:abc123" has 1 version
    And the "normalized" transcript of "yt:abc123" in "zh-Hans" has 120 cues

  @NRM-03 @NRM-R8
  Scenario: The normalized transcript points back to its source
    Given "yt:abc123" has auto captions in "en"
    And the item "yt:abc123" has been processed up to "normalize"
    Then the "normalized" artifact of "yt:abc123" has the provenance:
      | field        | value      |
      | parents.0    | source     |
      | impl_version | 1          |
