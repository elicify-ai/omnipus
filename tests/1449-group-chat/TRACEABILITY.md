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
| S-13 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_SteeringCommandIsRefusedWithTheMemberLink`, `pkg/rooms/rooms_test.go::TestCommands_SteeringRefused`, `pkg/gateway/wu15_rooms_commands_test.go::TestWU15Rooms_SteeringAndMembershipCommandsRefuseWithMainLinks` | May already hold. The oracle is the hyphen sentence from S-13 plus the main-chat link the scenario also requires. The earlier em-dash expectation was the wrong oracle |
| S-61 | `pkg/rooms/groupchat1449_red_test.go::TestGroupChat1449_NewGroupExposesInvitationStatus` | RED: `invitation_context_status` is absent |
| T-55, TD-8, D27, S-21, S-24, S-152 | `pkg/rooms/groupchat1449_contract_red_test.go::TestGroupChat1449_ContractStatesTheClampedTextLimit`, `pkg/api/generated/groupchat1449_stored_entry_red_test.go::TestGroupChat1449_StoredEntryOf100000BytesPassesBothValidators`, `pkg/rooms/groupchat1449_answer_files_red_test.go::TestGroupChat1449_StoredEntryLongerThanTodaysLimitStillOpens` | RED on the read bound: both `RoomEntry.text` copies still have maxLength 65536. The write limit stays 65536 on `RoomMessageRequest.text`. A stored 100,000-byte entry must still open in full, and a new message of that size is 422 naming 65536 |
| US-9 contract, D26 | `pkg/rooms/groupchat1449_contract_red_test.go::TestGroupChat1449_ContractCarriesQuestionAndLibraryAssociation` | RED: `room_ref`, `origin_label`, `chat_copy`, `library_path` are absent |
| D26, S-22 | `pkg/rooms/groupchat1449_contract_red_test.go::TestGroupChat1449_ContractDescribesLibraryDeleteAndWorkspaceUpload` | RED: deleteLibraryEntry does not mention the chat copy; uploadFiles has no workspace_id and no 503 |
| S-29, S-31 | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_AnswerRequestSchemaDeclaresAtMost16Media` | RED: parameters have no media array |
| S-34 | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_AnswerMediaFromAnotherWorkspacePostsNothing` | RED: the extra media argument is ignored and the reply is delivered |
| S-119 tool half | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_AnswerTextOverTheLimitPostsNothing` | RED: 65,537 bytes is passed to the reply router with no limit error |
| S-31, DS-12 row 18 | `pkg/tools/groupchat1449_answer_media_red_test.go::TestGroupChat1449_SeventeenAnswerFilesAreRefusedBeforeDelivery` | RED: 17 media entries are ignored and the reply is delivered |
| S-71, S-79, S-83, S-151, DS-8 | `pkg/agent/groupchat1449_origin_red_test.go::TestGroupChat1449_RoomOriginOnlyWhenTheStepAnswersThatGroupAlone` | RED for own-chat+group and for two groups: the room reference is kept. Channel-only mix may already hold |
| S-118 HTTP, S-150, S-18, S-24, S-26, S-61, D21, A-12 | `pkg/gateway/groupchat1449_http_red_test.go::TestGroupChat1449_HTTPMessageRules` | RED: validation off posts a message that carries an unknown media field; validation on returns 400 for media and for empty text; steer_body 131072 accepts 65,537 bytes; GET room has no invitation status. A-12: a 65,537-character message is 422 naming 65536 with validation on and off, never 400. The no-stop route may already hold |
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
| S-50 | same file, `S-50 shows` | The live-line case. The Participants control is the header-slot case below, not this file |
| S-98 | `src/components/chat/ChatHeaderSlot.test.tsx` `a room page shows the Participants button once the room loads` | May already hold once the room record arrives: one button named `Participants, 2 members`, no Add participant, no feed-kind label, and no border or background class on the slot. The button stays absent while the room is still loading |
| T-39, S-50 | `src/components/ui/avatar-stack.test.tsx` `AvatarStack draws up to max avatars and a +N marker for the rest` | BLOCKED until `AvatarStack` exists, then 2/3/4/10 follow S-50 |
| S-44, DS-9 | `src/store/groupchat1449.history-limit.test.ts` `S-44 sends the fill page size` | RED: `loadOlder` does not send the caller's in-range limit (20 and 100). A call with no size is not the oracle |
| S-43 | same file, `S-43 makes no history call` | May already hold when `has_older` is false |
| S-147 | `src/components/library/groupchat1449.delete-confirm.test.tsx` `S-147 names the chat copy` | RED: the dialog never mentions a chat copy |
| S-147 no copy | same file, `keeps today's dialog` | May already hold |
| S-148 | same file, `S-148 keeps the file listed` | May already hold if the server message is shown in the existing banner. The confirm-button stays so a repeat is possible |
| S-106, T-56 | `tests/e2e/groupchat1449-remote-image.spec.ts` | RED once a gateway is up: the image is not rendered by the shared pipeline, so the accessible image is absent. The positive control is the document request |
| DS-4 rows 1–14, TD-1, TD-2, TD-9, FR-046 | `pkg/rooms/groupchat1449_invitation_red_test.go` `TestGroupChat1449_InvitationSummary*` | Time 44/45 ready, 46 discarded; 4,096 cut rules; 0.02 dollars is 20,000 micro-dollars counted once; no call and a provider error write no cost; an unpriced call sets the flag and adds 0. The summary is carried once to the other member and never to the origin agent. A message during generation is not held. Restart, delete and removing the origin agent discard a late result |
| DS-5 rows 1–8 and 10, FR-040, T-21 | `pkg/agent/groupchat1449_invitation_red_test.go` `TestGroupChat1449_Invitation*` | Newest 40 entries; 16,384 bytes kept whole; 16,385 cut at an entry boundary. Registered `sk-REG-123` and the SEC-16 key shape are absent from the model input and from the scrubbed text. A tool-only origin chat does not call the model. One `llm_usage` record names the origin agent and that session; the origin transcript and stored usage stay unchanged |
| DS-1 row 32, DS-12 rows 16–19, A-4, A-9, TD-3, TD-4 | `pkg/rooms/groupchat1449_answer_files_red_test.go` `TestGroupChat1449_DuplicateMediaRefIsListedTwiceAndRetainedOnce`, `TestGroupChat1449_AnswerListsTheSameFileTwice`, `TestGroupChat1449_AnswerFileCountAndSizeBounds` | The same file listed twice is shown twice and retained once. 16 files are accepted and staging is empty after commit. 17 files and 100 MB + 1 byte are `ErrAnswerFilesRefused` before resolve or Open, and nothing is staged or posted |
| S-29, FR-020 | `pkg/tools/answer_request_test.go::TestAnswerRequest_SchemaIsMinimalAndExact` | May already hold. Exactly three properties: `request_id`, `text`, `media`. `media` is an array with maxItems 16. Required stays `request_id` and `text` |
| S-22 | `pkg/gateway/rest_upload_workspace_library_test.go::TestUpload_WorkspaceLibraryLoadFailure_Returns503NotSilentFallback` | May already hold. A workspace library that exists but will not load is 503 and does not fall through to the session uploads directory |
| D23, S-83, DS-8 row 2 | `pkg/gateway/train3_fix1_joined_origin_test.go::TestTrain3Fix1_HeldGroupRequestJoiningAWebTurnDropsTheGroupReference` | RED if the resumed web turn that also consumes a held group request keeps `for group` or a room reference. The card stays in the member's main session |

## Not covered here, on purpose

| Spec row | Why |
|---|---|
| US-14 / DS-13 except S-147, S-148 and the contract wording | P2. The association does not exist, so a handler test cannot yet fail on "both copies removed" without a new production symbol |
| DS-4 rows 15–21 | Reservation races against receiver facts. They need the capture ledger, not the summary job. Not silently skipped: named here so the gap stays visible |
| DS-5 row 9 | A password written as plain prose ("the password is hunter2") is an accepted gap (D18, #1467). The tests do not assert that it is removed or that it is kept |
| S-20, S-27 happy path | `RoomMessageRequest` has no media field, so a success post cannot be written against the generated type. The negative (bad ref stores nothing) is the HTTP test |
| S-71 live card, S-74, S-75, S-80 | Need the running approval and question pipeline. Carried as UAT-10, UAT-13, UAT-23, UAT-24 |
| S-40 10,005-entry paging, T-47, T-48, T-49, T-50 | Browser rows. T-56 is the one Playwright spec in this pack; the others stay on the UAT rows until a server is up for the frozen run |

Proof of failability item 1 (seen red) is deferred to the frozen script. Items 2 and 3 and the mutation gate are deferred to CHECK.
