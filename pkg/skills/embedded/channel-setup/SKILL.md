---
name: channel-setup
description: Configure and verify a communication channel under its outbound-send policy.
---
# Channel Setup
## Prerequisites
Know the channel, approved credential reference, and enable/test intent.
## Steps
1. Inspect with list_channels and configure with configure_channel.
2. Enable with enable_channel when requested.
3. Probe with test_channel and state the outbound Ask policy.
## Expected output
A usable tested channel or explicit incomplete setup.
## Stop and handoff
Do not send a real external message merely to prove configuration unless separately approved.
