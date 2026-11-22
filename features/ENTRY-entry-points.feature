@M4
Feature: Other ways to add a video
  As the owner, I want a bookmarklet and a share target that hand a link to
  Notesteme, so that adding a video takes one click on the desktop and one share
  on my phone.

  Background:
    Given the owner password is "correct horse"
    And the owner is signed in

  # ---------------------------------------------------------------------------
  # ENTRY-01：bookmarklet 與 /add
  # ---------------------------------------------------------------------------

  @ENTRY-01 @ENTRY-R2
  Scenario: The add page opens for the owner without starting anything
    When the browser opens "/add?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3Dabc123xxxxx" signed in as the owner
    Then the response status is 200
    And the response header "Content-Type" contains "text/html"
    And the library contains 0 items

  @ENTRY-01 @ENTRY-R2
  Scenario: The add page sends a signed-out browser to the sign-in page and back
    When the browser opens "/add?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3Dabc123xxxxx"
    Then the response status is 303
    And the browser is sent to "/login?next=%2Fadd%3Furl%3Dhttps%253A%252F%252Fwww.youtube.com%252Fwatch%253Fv%253Dabc123xxxxx"

  @ENTRY-01 @ENTRY-02 @ENTRY-R7
  Scenario Outline: Entry pages cannot be framed and change nothing
    When the browser opens "<path>" signed in as the owner
    Then the response header "Content-Security-Policy" contains "frame-ancestors 'none'"
    And the response header "Cache-Control" contains "no-store"
    And the library contains 0 items

    Examples:
      | path                                                         |
      | /add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                 |
      | /share?text=Watch+this%3A+https%3A%2F%2Fyoutu.be%2Fabc123xxxxx |
      | /login                                                       |

  @ui @manual @ENTRY-01 @ENTRY-R1
  Scenario: The bookmarklet only sends the address of the current page
    Given the owner opens the bookmarklet section of the settings page
    When the owner drags the bookmarklet to the bookmarks bar and inspects its code
    Then the code only reads "location.href" and opens "<origin>/add?url=…" in a new tab
    And the code contains no token, no cookie and no access to the page's document

  @ui @manual @ENTRY-01 @ENTRY-R1 @ENTRY-R2
  Scenario: Clicking the bookmarklet on a video page fills in the add drawer
    Given the owner is watching a YouTube video in another tab
    When the owner clicks the bookmarklet
    Then Notesteme opens in a new tab with the add drawer showing that address and its estimated cost
    And nothing is processed until the owner presses "開始處理"

  @ui @manual @ENTRY-01 @ENTRY-R2
  Scenario: An address that is not http or https is ignored
    When the owner opens "/add?url=javascript%3Aalert(1)"
    Then the add drawer is empty and shows "書籤或分享的網址無效，已忽略。"

  # ---------------------------------------------------------------------------
  # ENTRY-02：PWA share target
  # ---------------------------------------------------------------------------

  @ENTRY-02 @ENTRY-R3
  Scenario: The web app manifest declares the share target
    When the browser opens "/manifest.webmanifest"
    Then the response status is 200
    And the response header "Content-Type" contains "application/manifest+json"
    And the response contains:
      """
      { "name": "Notesteme", "start_url": "/", "scope": "/", "display": "standalone",
        "share_target": { "action": "/share", "method": "GET",
                          "params": { "title": "title", "text": "text", "url": "url" } } }
      """

  @ENTRY-02 @ENTRY-R3
  Scenario: The service worker is served without caching
    When the browser opens "/sw.js"
    Then the response status is 200
    And the response header "Content-Type" contains "text/javascript"
    And the response header "Cache-Control" contains "no-cache"

  @ENTRY-02 @ENTRY-R4
  Scenario Outline: The first link in a share is handed to the add page
    When the browser opens "/share?<query>" signed in as the owner
    Then the response status is 303
    And the browser is sent to "<target>"

    Examples:
      | query                                                                                                   | target                                                          |
      | url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx&text=&title=                                                    | /add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                   |
      | text=Watch+this%3A+https%3A%2F%2Fyoutu.be%2Fabc123xxxxx+%F0%9F%91%8D&title=Kafka                         | /add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                   |
      | text=%E7%9C%8B%E9%80%99%E5%80%8B%EF%BC%88https%3A%2F%2Fwww.bilibili.com%2Fvideo%2FBV1co411c7mA%EF%BC%89 | /add?url=https%3A%2F%2Fwww.bilibili.com%2Fvideo%2FBV1co411c7mA  |
      | text=first+https%3A%2F%2Fa.example.com%2Fx.+then+https%3A%2F%2Fb.example.com%2Fy                        | /add?url=https%3A%2F%2Fa.example.com%2Fx                        |
      | url=javascript%3Aalert%281%29&text=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                                  | /add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                   |
      | text=&title=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                                                         | /add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx                   |

  @ENTRY-02 @ENTRY-R4
  Scenario: A share without any link says so
    When the browser opens "/share?text=just+some+words&title=Hello" signed in as the owner
    Then the response status is 303
    And the browser is sent to "/add?share_error=no_url"
    And the logs do not contain "just some words"

  @ENTRY-02 @ENTRY-R5
  Scenario: Sharing while signed out goes through the sign-in page
    When the browser opens "/share?text=Watch+this%3A+https%3A%2F%2Fyoutu.be%2Fabc123xxxxx"
    Then the response status is 303
    And the browser is sent to "/login?next=%2Fadd%3Furl%3Dhttps%253A%252F%252Fyoutu.be%252Fabc123xxxxx"

  @ENTRY-02 @ENTRY-R5 @ENTRY-R6
  Scenario: Signing in continues to the shared link
    When someone signs in with the password "correct horse" to continue to "/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx"
    Then the response status is 200
    And the response contains:
      """
      { "next": "/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx" }
      """

  @ENTRY-02 @ENTRY-R6
  Scenario Outline: Sign-in never continues to another site
    When someone signs in with the password "correct horse" to continue to "<next>"
    Then the response status is 200
    And the response contains:
      """
      { "next": "/" }
      """

    Examples:
      | next                          |
      | https://evil.example.com/     |
      | //evil.example.com/x          |
      | /\\evil.example.com/x         |
      | javascript:alert(1)           |
      | evil.example.com/x            |

  @ENTRY-02 @ENTRY-R6
  Scenario: A wrong password does not reveal where it would continue
    When someone signs in with the password "wrong" to continue to "/add?url=https%3A%2F%2Fyoutu.be%2Fabc123xxxxx"
    Then the response status is 401
    And the response contains:
      """
      { "message": "密碼不正確。" }
      """

  @ui @manual @ENTRY-02 @ENTRY-R3 @ENTRY-R4 @ENTRY-R5
  Scenario: Sharing from the YouTube app on Android
    Given Notesteme is installed as an app on an Android phone and the owner is signed out
    When the owner shares a video from the YouTube app to Notesteme
    Then the sign-in page opens
    And after signing in the add drawer shows the shared video address
