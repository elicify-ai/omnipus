# #1449 RED traceability

Oracles are scenario, dataset and constraint rows in `docs/internal/specs/1449-group-chat-spec.md`. "Expected RED" means the current code fails that oracle. "May already hold" means the current code already does this; the test is the regression oracle and is not red-before-green proof. Runs are held (founder rule); nothing below was executed in this turn.

| Spec row | Test | Expected RED reason |
|---|---|---|
| S-118, DS-1 rows 18–21 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_PersonMessageSizeAtDefaultLimit` | May already hold at the default 65,536 limit |
| S-150, DS-1 rows 26–31 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_OperatorSettingCannotRaiseTheGroupLimit` | RED when steer_body is 131072: `bodyLimit` returns that value and a 65,537-byte message is stored. The 1,000-byte rows may already hold |
| S-18, DS-1 rows 1–3 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_EmptyAndWhitespacePersonMessageIsRefused` | May already hold (empty text is refused before media exists) |
| DS-1 rows 14–15 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_MentionCountBounds` | May already hold |
| S-119, S-150 answer half | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_AgentAnswerUsesTheClampedLimitAndPostsNothingWhenOver` | RED: with steer_body 131072 the answer is posted and mirrored |
| S-115 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_PausedMessageIsSavedAndNotDelivered` | May already hold |
| S-13 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_SteeringCommandIsRefusedWithTheMemberLink` | RED: the server text uses an em dash and appends the session id; the scenario sentence is `Rooms don't steer agents - use Mia's main chat` |
| S-61 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_NewGroupExposesInvitationStatus` | RED: `invitation_context_status` is absent |
| T-55, D24, D27, S-21, S-24 | `pkg/rooms/groupchat1449_contract_red_test.go::TestGroupChat1449_ContractStatesTheClampedTextLimit` | RED: schemas still say maxLength 5242880, text minLength 1, no media / attachments |
| US-9 contract, D26 | `pkg/rooms/groupchat1449_contract_red_test.go::TestGroupChat1449_ContractCarriesQuestionAndLibraryAssociation` | RED: `room_ref`, `origin_label`, `chat_copy`, `library_path` are absent |
| D26, S-22 | `pkg/rooms/groupchat1449_contract_red_test.go::TestGroupChat1449_ContractDescribesLibraryDeleteAndWorkspaceUpload` | RED: deleteLibraryEntry does not mention the chat copy; uploadFiles has no workspace_id and no 503 |
| S-29, S-31 | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_AnswerRequestSchemaDeclaresAtMost16Media` | RED: parameters have no media array |
| S-34 | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_AnswerMediaFromAnotherWorkspacePostsNothing` | RED: the extra media argument is ignored and the reply is delivered |
| S-119 tool half | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_AnswerTextOverTheLimitPostsNothing` | RED: 65,537 bytes is passed to the reply router with no limit error |
| S-31, DS-12 row 18 | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_SeventeenAnswerFilesAreRefusedBeforeDelivery` | RED: 17 media entries are ignored and the reply is delivered |
| S-71, S-79, S-83, S-151, DS-8 | `pkg/agent/groupchat1449_origin_red_test.go::TestGroupChat1449_RoomOriginOnlyWhenTheStepAnswersThatGroupAlone` | RED for own-chat+group and for two groups: the room reference is kept. Channel-only mix may already hold |
| S-118 HTTP, S-150, S-18, S-24, S-26, S-61, D21 | `pkg/gateway/groupchat1449_http_red_test.go::TestGroupChat1449_HTTPMessageRules` | RED: validation off posts a message that carries an unknown media field; validation on returns 400 for media and for empty text; steer_body 131072 accepts 65,537 bytes; GET room has no invitation status. The no-stop route may already hold |
| S-01 | `src/components/rooms/groupchat1449.display.test.tsx` `S-01 renders a markdown table` | RED: the transcript is a plain pre-wrap paragraph, no table |
| S-02 | same file, `S-02 copies the code block` | RED: there is no code-block Copy control (`Copy code to clipboard`). The oracle is that control, not every button whose name contains "copy" |
| S-07 | same file, `S-07 shows each agent name and avatar` | RED: no avatar mark, and the person's row is labelled "You" and not right-aligned (`flex-row-reverse` counts). The avatar is the shared mark beside the name, not a named image |
| S-08 | same file, `S-08 keeps the muted after-pause mark` | May already hold ("after pause" is already rendered) |
| S-09 | same file, `S-09 does not show another chat's tool calls` | May already hold: RoomScreen does not read the session transcript. This becomes the gate when the shared rows land |
| S-104 | same file, `S-104 shows an agent script tag` | May already hold on the plain-text renderer. It fails if a later renderer inserts a script element |
| S-105 | same file, `S-105 strikes unsafe link schemes` | RED: the address is plain text. Strike is the shared `line-through` class (jsdom does not compute Tailwind). A normal https link must stay a new-tab link without referrer access |
| S-106 | same file, `S-106 does not request a remote image` | The "no request" half may already hold because no image is rendered. The browser half is the Playwright spec |
| S-41 | same file, `S-41 shows Return to latest only while earlier messages are being read` | RED: opening on the newest message must not show the control. It is required only after the reader is on earlier messages, then it disappears. The group screen has no such reader yet |
| S-39 | same file, `S-39 puts the message box in the same centred column` | RED: the group composer is not inside the normal chat `max-w-3xl` column |
| S-10, D21 | `src/components/rooms/groupchat1449.composer.test.tsx` `S-10 has the normal card` | RED: no attach button. No model picker, Auto-approve, skills or Stop may already hold |
| S-19 | same file, `S-19 shows the group placeholder` | RED: placeholder is `Message the group — @ to address members` |
| S-12 | same file, `S-12 lists exactly the six group commands` | RED: typing "/" opens no command menu. A description beside a command is allowed; a seventh command is not |
| S-17 | same file, `S-17 disables sending` | RED: the notice says `Loading the conversation…`, not `waiting for the group` |
| S-108 | same file, `S-108 sends to the group` | May already hold |
| S-35 | `src/components/rooms/groupchat1449.live.test.tsx` `S-35 shows one line` | RED: the label is `working`, not `Jim is working...` |
| S-36 | same file, `S-36 replaces only the finished agent line` | RED: same label, so the replacement oracle fails |
| S-50, S-98 | same file, `S-50 shows` / `S-98 puts the Participants button` | BLOCKED until `ChatHeaderSlot` exists, then the header slot (not RoomScreen) must show the S-50 table and no bar, border or surface fill |
| T-39, S-50 | `src/components/ui/avatar-stack.test.tsx` `AvatarStack draws up to max avatars and a +N marker for the rest` | BLOCKED until `AvatarStack` exists, then 2/3/4/10 follow S-50 |
| S-44, DS-9 | `src/store/groupchat1449.history-limit.test.ts` `S-44 sends the fill page size` | RED: `loadOlder` does not send the caller's in-range limit (20 and 100). A call with no size is not the oracle |
| S-43 | same file, `S-43 makes no history call` | May already hold when `has_older` is false |
| S-147 | `src/components/library/groupchat1449.delete-confirm.test.tsx` `S-147 names the chat copy` | RED: the dialog never mentions a chat copy |
| S-147 no copy | same file, `keeps today's dialog` | May already hold |
| S-148 | same file, `S-148 keeps the file listed` | May already hold if the server message is shown in the existing banner. The confirm-button stays so a repeat is possible |
| S-106, T-56 | `tests/e2e/groupchat1449-remote-image.spec.ts` | RED once a gateway is up: the image is not rendered by the shared pipeline, so the accessible image is absent. The positive control is the document request |

## Not covered here, on purpose

| Spec row | Why |
|---|---|
| US-14 / DS-13 except S-147, S-148 and the contract wording | P2. The association does not exist, so a handler test cannot yet fail on "both copies removed" without a new production symbol |
| DS-4, DS-5 invitation lifecycle and filters | No summary function to call. S-61 is the compilable RED seam. A prose password is not asserted (D18, #1467) |
| S-20, S-27 happy path | `RoomMessageRequest` has no media field, so a success post cannot be written against the generated type. The negative (bad ref stores nothing) is the HTTP test |
| S-71 live card, S-74, S-75, S-80 | Need the running approval and question pipeline. Carried as UAT-10, UAT-13, UAT-23, UAT-24 |
| S-40 10,005-entry paging, T-47, T-48, T-49, T-50 | Browser rows. T-56 is the one Playwright spec in this pack; the others stay on the UAT rows until a server is up for the frozen run |

Proof of failability item 1 (seen red) is deferred to the frozen script. Items 2 and 3 and the mutation gate are deferred to CHECK.
