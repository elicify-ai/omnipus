---
name: disable-channel
description: Use when the user wants to disable a connected chat channel (W14).
metadata:
  display_name: Disable Channel (W14)
---
# Disable a channel (W14)

## Prerequisites
`tool:list_channels` for instances and their status.

## Steps
1. List the channels and confirm which one to disable.
2. Present one card: the instance and what stops (it stops answering; its configuration stays).
3. One Go / Change / Cancel question. Never call a change tool before Go.
4. After Go: `tool:disable_channel`.

## Expected output
The disable outcome — working / saved but not working: reason / failed: nothing saved.

## Stop and handoff
Cancel changes nothing. Removing or rebinding a channel belongs to `connect-channel` or the Settings forms, not here.
