# Flow file format (one YAML file per flow, docs/internal/architecture/as-is/source/flows/<id>.yaml)

id: web-chat-turn
title: "A message typed in the web chat becomes an answer"
summary: >   # 3-5 plain-English sentences: what happens, what it means for the user
participants:          # left-to-right order, 4-9 lanes max
  - {id: browser, label: "Web app (browser)", kind: person}     # kind: person|browser|server|worker|store|external
  - {id: ws, label: "Chat socket handler", kind: server, ref: "pkg/gateway/websocket_chat.go"}
steps:                 # in time order
  - phase: "1 · Send"                                 # optional phase header row
  - {from: browser, to: ws, label: "chat frame {content, session_id}", kind: async, ref: "pkg/gateway/websocket_chat.go::handleChat", verified: true}
    # kind: call (waits for answer) | async (fire and forget / queue) | return | store (write to disk/memory) | wait (blocks until X) | self
  - {over: ws, note: "checks sign-in token first", ref: "...", verified: true}
  - {loop: "repeat until the model stops asking for tools"}  # opens a frame; close with {end: true}
  - {alt: "if the provider errors"}  ...  {else: "otherwise"} ... {end: true}
stores:                # what is written where and when
  - {what: "transcript line", where: "~/.omnipus/agents/<id>/sessions/<sid>/<day>.jsonl", when: "after each message", ref: "..."}
failures:              # what happens when it goes wrong, each with ref
  - {case: "browser disconnects mid-turn", effect: "...", ref: "..."}
facts:                # key facts that are easy to get wrong, each with ref
  - {fact: "...", ref: "..."}
