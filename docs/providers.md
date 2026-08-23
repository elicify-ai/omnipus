# 🔌 Providers & Model Configuration

> Back to [README](../README.md)

### Providers

> [!NOTE]
> Voice transcription can use a configured multimodal model via `voice.model_name`. Groq Whisper remains available as a fallback when no voice model is configured.

| Provider     | Purpose                                 | Get API Key                                                  |
| ------------ | --------------------------------------- | ------------------------------------------------------------ |
| `gemini`     | LLM (Gemini direct)                     | [aistudio.google.com](https://aistudio.google.com)           |
| `zhipu`      | LLM (Zhipu direct)                      | [bigmodel.cn](https://bigmodel.cn)                           |
| `zai-coding` | LLM (Z.AI Coding Plan)                | [z.ai](https://z.ai/manage-apikey/apikey-list)           |
| `volcengine` | LLM(Volcengine direct)                  | [volcengine.com](https://www.volcengine.com/activity/codingplan?utm_campaign=Omnipus&utm_content=Omnipus&utm_medium=devrel&utm_source=OWO&utm_term=Omnipus)                 |
| `openrouter` | LLM (recommended, access to all models) | [openrouter.ai](https://openrouter.ai)                       |
| `anthropic`  | LLM (Claude direct)                     | [console.anthropic.com](https://console.anthropic.com)       |
| `openai`     | LLM (GPT direct)                        | [platform.openai.com](https://platform.openai.com)           |
| `deepseek`   | LLM (DeepSeek direct)                   | [platform.deepseek.com](https://platform.deepseek.com)       |
| `qwen`       | LLM (Qwen direct)                       | [dashscope.console.aliyun.com](https://dashscope.console.aliyun.com) |
| `groq`       | LLM + **Voice transcription** (Whisper) | [console.groq.com](https://console.groq.com)                 |
| `cerebras`   | LLM (Cerebras direct)                   | [cerebras.ai](https://cerebras.ai)                           |
| `vivgrid`    | LLM (Vivgrid direct)                    | [vivgrid.com](https://vivgrid.com)                           |
| `nvidia`     | LLM (NVIDIA NIM)                        | [build.nvidia.com](https://build.nvidia.com)                 |
| `minimax`    | LLM (Minimax direct)                    | [platform.minimaxi.com](https://platform.minimaxi.com)      |
| `avian`      | LLM (Avian direct)                      | [avian.io](https://avian.io)                                 |
| `mistral`    | LLM (Mistral direct)                    | [console.mistral.ai](https://console.mistral.ai)            |
| `longcat`    | LLM (Longcat direct)                    | [longcat.ai](https://longcat.ai)                             |
| `modelscope` | LLM (ModelScope direct)                 | [modelscope.cn](https://modelscope.cn)                       |
| `mimo`       | LLM (Xiaomi MiMo direct)                | [platform.xiaomimimo.com](https://platform.xiaomimimo.com)   |

### Model Configuration

The `providers` key holds an array of model entries, each shaped as `{"model_name": "<alias>", "model": "<vendor>/<model-id>"}` (e.g. `zhipu/glm-4.7`). Note: `providers` has always been the JSON key name — in the legacy v0 schema it was an *object* keyed by vendor; in the current v1 schema it is an *array* of model entries. The new shape supports per-entry credential references, multi-key failover, and per-agent model selection. (The legacy `model_list` key is still accepted and silently renamed to `providers` on load.)

This design also enables **multi-agent support** with flexible provider selection. Each agent can use its own LLM provider. You can configure primary and fallback models for resilience, distribute requests across multiple endpoints for load balancing, and manage all providers in one place through centralized configuration.

#### 📋 All Supported Vendors

| Vendor              | `model` Prefix    | Default API Base                                    | Protocol  | API Key                                                          |
| ------------------- | ----------------- |-----------------------------------------------------| --------- | ---------------------------------------------------------------- |
| **OpenAI**          | `openai/`         | `https://api.openai.com/v1`                         | OpenAI    | [Get Key](https://platform.openai.com)                           |
| **Anthropic**       | `anthropic/`      | `https://api.anthropic.com/v1`                      | Anthropic | [Get Key](https://console.anthropic.com)                         |
| **智谱 AI (GLM)**   | `zhipu/`          | `https://open.bigmodel.cn/api/paas/v4`              | OpenAI    | [Get Key](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) |
| **Z.AI Coding Plan** | `openai/`         | `https://api.z.ai/api/coding/paas/v4`              | OpenAI    | [Get Key](https://z.ai/manage-apikey/apikey-list) |
| **DeepSeek**        | `deepseek/`       | `https://api.deepseek.com/v1`                       | OpenAI    | [Get Key](https://platform.deepseek.com)                         |
| **Google Gemini**   | `gemini/`         | `https://generativelanguage.googleapis.com/v1beta`  | OpenAI    | [Get Key](https://aistudio.google.com/api-keys)                  |
| **Groq**            | `groq/`           | `https://api.groq.com/openai/v1`                    | OpenAI    | [Get Key](https://console.groq.com)                              |
| **通义千问 (Qwen)** | `qwen/`           | `https://dashscope.aliyuncs.com/compatible-mode/v1` | OpenAI    | [Get Key](https://dashscope.console.aliyun.com)                  |
| **NVIDIA**          | `nvidia/`         | `https://integrate.api.nvidia.com/v1`               | OpenAI    | [Get Key](https://build.nvidia.com)                              |
| **Ollama**          | `ollama/`         | `http://localhost:11434/v1`                         | OpenAI    | Local (no key needed)                                            |
| **OpenRouter**      | `openrouter/`     | `https://openrouter.ai/api/v1`                      | OpenAI    | [Get Key](https://openrouter.ai/keys)                            |
| **LiteLLM Proxy**   | `litellm/`        | `http://localhost:4000/v1`                          | OpenAI    | Your LiteLLM proxy key                                            |
| **VLLM**            | `vllm/`           | `http://localhost:8000/v1`                          | OpenAI    | Local                                                            |
| **Cerebras**        | `cerebras/`       | `https://api.cerebras.ai/v1`                        | OpenAI    | [Get Key](https://cerebras.ai)                                   |
| **VolcEngine (Doubao)** | `volcengine/`     | `https://ark.cn-beijing.volces.com/api/v3`          | OpenAI    | [Get Key](https://www.volcengine.com/activity/codingplan?utm_campaign=Omnipus&utm_content=Omnipus&utm_medium=devrel&utm_source=OWO&utm_term=Omnipus)                        |
| **神算云**          | `shengsuanyun/`   | `https://router.shengsuanyun.com/api/v1`            | OpenAI    | -                                                                |
| **Vivgrid**         | `vivgrid/`        | `https://api.vivgrid.com/v1`                        | OpenAI    | [Get Key](https://vivgrid.com)                                   |
| **LongCat**         | `longcat/`        | `https://api.longcat.chat/openai`                   | OpenAI    | [Get Key](https://longcat.chat/platform)                         |
| **ModelScope (魔搭)**| `modelscope/`    | `https://api-inference.modelscope.cn/v1`            | OpenAI    | [Get Token](https://modelscope.cn/my/tokens)                     |
| **Xiaomi MiMo**     | `mimo/`           | `https://api.xiaomimimo.com/v1`                     | OpenAI    | [Get Key](https://platform.xiaomimimo.com)                       |
| **Azure OpenAI**    | `azure/`          | `https://{resource}.openai.azure.com`               | Azure     | [Get Key](https://portal.azure.com)                              |

#### Basic Configuration

```json
{
  "providers": [
    {
      "model_name": "ark-code-latest",
      "model": "volcengine/ark-code-latest",
      "api_key_ref": "VOLCENGINE_API_KEY"
    },
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_key_ref": "OPENAI_API_KEY"
    },
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_key_ref": "ANTHROPIC_API_KEY"
    },
    {
      "model_name": "glm-4.7",
      "model": "zhipu/glm-4.7",
      "api_key_ref": "ZHIPU_API_KEY"
    }
  ],
  "agents": {
    "defaults": {
      "model_name": "gpt-5.4"
    }
  }
}
```

> **Note:** `api_key_ref` is the production schema: it references a named credential in `credentials.json` (which is decrypted at load and injected into the process environment). The legacy plaintext `api_key` field is silently dropped by the loader.

#### Voice Transcription

You can configure a dedicated model for audio transcription with `voice.model_name`. This lets you reuse existing multimodal providers that support audio input instead of relying only on Groq.

If `voice.model_name` is not configured, Omnipus will continue to fall back to Groq transcription when a Groq API key is available.

```json
{
  "providers": [
    {
      "model_name": "voice-gemini",
      "model": "gemini/gemini-2.5-flash",
      "api_key_ref": "GEMINI_API_KEY"
    },
    {
      "model_name": "groq",
      "model": "groq/llama-3.3-70b-versatile",
      "api_key_ref": "GROQ_API_KEY"
    }
  ],
  "voice": {
    "model_name": "voice-gemini",
    "echo_transcription": false
  }
}
```

#### Vendor-Specific Examples

**OpenAI**

```json
{
  "model_name": "gpt-5.4",
  "model": "openai/gpt-5.4",
  "api_key_ref": "OPENAI_API_KEY"
}
```

**VolcEngine (Doubao)**

```json
{
  "model_name": "ark-code-latest",
  "model": "volcengine/ark-code-latest",
  "api_key_ref": "VOLCENGINE_API_KEY"
}
```

**智谱 AI (GLM)**

```json
{
  "model_name": "glm-4.7",
  "model": "zhipu/glm-4.7",
  "api_key_ref": "ZHIPU_API_KEY"
}
```

**Z.AI Coding Plan (GLM)**
> Z.AI and 智谱 AI are two brands of the same provider. For the Z.AI Coding Plan use the `openai` model key and the api base as follows, rather than the zhipu config
```json
{
  "model_name": "glm-4.7",
  "model": "openai/glm-4.7",
  "api_key_ref": "ZAI_API_KEY",
  "api_base": "https://api.z.ai/api/coding/paas/v4"
}
```

**DeepSeek**

```json
{
  "model_name": "deepseek-chat",
  "model": "deepseek/deepseek-chat",
  "api_key_ref": "DEEPSEEK_API_KEY"
}
```

**Anthropic (with API key)**

```json
{
  "model_name": "claude-sonnet-4.6",
  "model": "anthropic/claude-sonnet-4.6",
  "api_key_ref": "ANTHROPIC_API_KEY"
}
```

> Set your key with: `omnipus credentials set ANTHROPIC_API_KEY <your-key>`

**Anthropic Messages API (native format)**

For direct Anthropic API access or custom endpoints that only support Anthropic's native message format:

```json
{
  "model_name": "claude-opus-4-6",
  "model": "anthropic-messages/claude-opus-4-6",
  "api_key_ref": "ANTHROPIC_API_KEY",
  "api_base": "https://api.anthropic.com"
}
```

> Use `anthropic-messages` protocol when:
> - Using third-party proxies that only support Anthropic's native `/v1/messages` endpoint (not OpenAI-compatible `/v1/chat/completions`)
> - Connecting to services like MiniMax, Synthetic that require Anthropic's native message format
> - The existing `anthropic` protocol returns 404 errors (indicating the endpoint doesn't support OpenAI-compatible format)
>
> **Note:** The `anthropic` protocol uses OpenAI-compatible format (`/v1/chat/completions`), while `anthropic-messages` uses Anthropic's native format (`/v1/messages`). Choose based on your endpoint's supported format.

**Ollama (local)**

```json
{
  "model_name": "llama3",
  "model": "ollama/llama3"
}
```

**Custom Proxy/API**

```json
{
  "model_name": "my-custom-model",
  "model": "openai/custom-model",
  "api_base": "https://my-proxy.com/v1",
  "api_key_ref": "CUSTOM_PROXY_KEY",
  "request_timeout": 300
}
```

**LiteLLM Proxy**

```json
{
  "model_name": "lite-gpt4",
  "model": "litellm/lite-gpt4",
  "api_base": "http://localhost:4000/v1",
  "api_key_ref": "LITELLM_PROXY_KEY"
}
```

Omnipus strips only the outer `litellm/` prefix before sending the request, so proxy aliases like `litellm/lite-gpt4` send `lite-gpt4`, while `litellm/openai/gpt-4o` sends `openai/gpt-4o`.

**Z.AI Coding Plan**

If the standard Zhipu endpoint (`https://open.bigmodel.cn/api/paas/v4`) returns 429 (code 1113: insufficient balance), try using the Z.AI Coding Plan endpoint instead:

```json
{
  "model_name": "glm-4.7",
  "model": "openai/glm-4.7",
  "api_key_ref": "ZHIPU_API_KEY",
  "api_base": "https://api.z.ai/api/coding/paas/v4"
}
```

**Note:** The Z.AI Coding Plan endpoint and standard Zhipu endpoint use the same API key format but have separate billing. If you encounter 429 errors with the standard Zhipu endpoint, the Z.AI Coding Plan endpoint may have available balance.

#### Load Balancing

Configure multiple endpoints for the same model name—Omnipus will automatically round-robin between them:

```json
{
  "providers": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api1.example.com/v1",
      "api_key_ref": "OPENAI_KEY_1"
    },
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api2.example.com/v1",
      "api_key_ref": "OPENAI_KEY_2"
    }
  ]
}
```

#### Automatic Model Failover (Cascade)

Omnipus already supports automatic failover when you configure `primary` + `fallbacks` in the agent model settings.
The runtime fallback chain retries the next candidate for retriable failures such as HTTP `429`, quota/rate-limit errors, and timeout errors.
It also applies cooldown tracking per candidate to avoid immediately retrying a recently failed target.

```json
{
  "providers": [
    {
      "model_name": "qwen-main",
      "model": "openai/qwen3.5:cloud",
      "api_base": "https://api.example.com/v1",
      "api_key_ref": "QWEN_MAIN_KEY"
    },
    {
      "model_name": "deepseek-backup",
      "model": "deepseek/deepseek-chat",
      "api_key_ref": "DEEPSEEK_BACKUP_KEY"
    },
    {
      "model_name": "gemini-backup",
      "model": "gemini/gemini-2.5-flash",
      "api_key_ref": "GEMINI_BACKUP_KEY"
    }
  ],
  "agents": {
    "defaults": {
      "model": {
        "primary": "qwen-main",
        "fallbacks": ["deepseek-backup", "gemini-backup"]
      }
    }
  }
}
```

If you use key-level failover for the same model, Omnipus can chain through additional key-backed candidates before moving to cross-model backups.

#### Migration from Legacy `providers` Config

The old `providers` configuration is **deprecated** but still supported for backward compatibility.

**Old Config (deprecated):**

```json
{
  "providers": {
    "zhipu": {
      "api_key_ref": "ZHIPU_API_KEY",
      "api_base": "https://open.bigmodel.cn/api/paas/v4"
    }
  },
  "agents": {
    "defaults": {
      "provider": "zhipu",
      "model_name": "glm-4.7"
    }
  }
}
```

**New Config (recommended):**

```json
{
  "providers": [
    {
      "model_name": "glm-4.7",
      "model": "zhipu/glm-4.7",
      "api_key_ref": "ZHIPU_API_KEY"
    }
  ],
  "agents": {
    "defaults": {
      "model_name": "glm-4.7"
    }
  }
}
```

For detailed migration guide, see [migration/model-list-migration.md](migration/model-list-migration.md).

### Provider Architecture

Omnipus routes providers by protocol family. The **OpenAI-compatible** protocol covers OpenRouter, OpenAI-compatible gateways, Groq, Zhipu, and vLLM-style endpoints. The **Anthropic** protocol handles Claude-native API behavior. The **Codex/OAuth** path covers the OpenAI OAuth/token authentication route.

This keeps the runtime lightweight while making new OpenAI-compatible backends mostly a config operation (`api_base` + `api_key`).

<details>
<summary><b>Zhipu</b></summary>

**1. Get API key and base URL**

Get [API key](https://bigmodel.cn/usercenter/proj-mgmt/apikeys)

**2. Configure**

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.omnipus/workspace",
      "model_name": "glm-4.7",
      "max_tokens": 32768,
      "temperature": 0.7,
      "max_tool_iterations": 50
    }
  },
  "providers": {
    "zhipu": {
      "api_key_ref": "ZHIPU_API_KEY",
      "api_base": "https://open.bigmodel.cn/api/paas/v4"
    }
  }
}
```

**3. Run**

```bash
omnipus mia "Hello"
```

</details>

<details>
<summary><b>Full config example</b></summary>

```json
{
  "agents": {
    "defaults": {
      "model_name": "anthropic/claude-opus-4-6"
    }
  },
  "session": {
    "dm_scope": "per-channel-peer"
  },
  "providers": [
    {
      "model_name": "openrouter-default",
      "model": "openrouter/auto",
      "api_key_ref": "OPENROUTER_API_KEY"
    },
    {
      "model_name": "groq",
      "model": "groq/llama-3.3-70b-versatile",
      "api_key_ref": "GROQ_API_KEY"
    }
  ],
  "voice": {
    "model_name": "voice-gemini",
    "echo_transcription": false
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "123456:ABC...",
      "allow_from": ["123456789"]
    },
    "discord": {
      "enabled": true,
      "token": "",
      "allow_from": [""]
    },
    "whatsapp": {
      "enabled": false,
      "bridge_url": "ws://localhost:3001",
      "use_native": false,
      "session_store_path": "",
      "allow_from": []
    },
    "feishu": {
      "enabled": false,
      "app_id": "cli_xxx",
      "app_secret": "xxx",
      "encrypt_key": "",
      "verification_token": "",
      "allow_from": []
    },
    "qq": {
      "enabled": false,
      "app_id": "",
      "app_secret": "",
      "allow_from": []
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": false,
        "api_key_ref": "BRAVE_API_KEY",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      },
      "perplexity": {
        "enabled": false,
        "api_key_ref": "PERPLEXITY_API_KEY",
        "max_results": 5
      },
      "searxng": {
        "enabled": false,
        "base_url": "http://localhost:8888",
        "max_results": 5
      }
    },
    "cron": {
      "exec_timeout_minutes": 5
    }
  }
}
```

</details>

---

## 📝 API Key Comparison

| Service          | Pricing                  | Use Case                              |
| ---------------- | ------------------------ | ------------------------------------- |
| **OpenRouter**   | Free: 200K tokens/month  | Multiple models (Claude, GPT-4, etc.) |
| **Volcengine CodingPlan** | ¥9.9/first month | Best for Chinese users, multiple SOTA models (Doubao, DeepSeek, etc.) |
| **Zhipu**        | Free: 200K tokens/month  | Suitable for Chinese users                |
| **Brave Search** | $5/1000 queries          | Web search functionality              |
| **SearXNG**      | Free (self-hosted)       | Privacy-focused metasearch (70+ engines) |
| **Groq**         | Free tier available      | Fast inference (Llama, Mixtral)       |
| **Cerebras**     | Free tier available      | Fast inference (Llama, Qwen, etc.)    |
| **LongCat**      | Free: up to 5M tokens/day | Fast inference                       |
| **ModelScope**   | Free: 2000 requests/day  | Inference (Qwen, GLM, DeepSeek, etc.) |

---

<div align="center">
  <img src="assets/logo.jpg" alt="Omnipus Meme" width="512">
</div>
