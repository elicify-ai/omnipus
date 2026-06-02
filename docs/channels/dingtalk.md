> Back to [Channels](../channels.md)

# DingTalk

DingTalk (钉钉) is Alibaba's enterprise communication platform. Omnipus connects via the [DingTalk Stream SDK](https://github.com/open-dingtalk/dingtalk-stream-sdk-go), which maintains a persistent WebSocket connection — no public webhook endpoint required.

## Configuration

```json
{
  "channels": {
    "dingtalk": {
      "enabled": true,
      "client_id": "YOUR_CLIENT_ID",
      "client_secret_ref": "dingtalk_client_secret",
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      },
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Activate the DingTalk channel. |
| `client_id` | string | Yes | Client ID of the DingTalk application (from the app's credential page). |
| `client_secret_ref` | string | Yes | Key name in the Omnipus credential store holding the Client Secret. |
| `allow_from` | array | No | User ID allowlist. Empty means all users are accepted. |
| `group_trigger` | object | No | Controls when the bot responds in group chats. `mention_only: true` restricts responses to @-mentions; `prefixes` lists trigger prefixes. |
| `reasoning_channel_id` | string | No | Channel or chat ID where extended reasoning traces are sent (separate from the main reply). |

## Setup

1. Go to the [DingTalk Open Platform](https://open.dingtalk.com/) and sign in.
2. Create an **internal enterprise application** (企业内部应用).
3. On the application's credential page, copy the **Client ID** and generate a **Client Secret**.
4. Store the Client Secret in the Omnipus credential store under the key you choose for `client_secret_ref` (e.g. `omnipus credentials set dingtalk_client_secret`).
5. Under **Message receiving mode** (消息接收模式), select **Stream Mode** (流模式). No webhook URL or inbound firewall rule is needed.
6. Grant the application the **Robot send message** (机器人发送消息) permission scope.
7. Set `client_id` in `config.json` to the Client ID obtained in step 3.

## Notes

- **Stream Mode only.** The channel uses `github.com/open-dingtalk/dingtalk-stream-sdk-go` and does not support webhook mode.
- **Outbound.** Replies use the per-session webhook URL provided by the DingTalk platform in each incoming event; no additional send API key is needed beyond `client_id` / `client_secret_ref`.
- **Group chats.** Use `group_trigger.mention_only: true` or `group_trigger.prefixes` to avoid the bot responding to every message in a group.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
