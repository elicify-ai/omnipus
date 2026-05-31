# Security Configuration

## Overview

Omnipus supports separating sensitive data (API keys, tokens, secrets, passwords) from the main configuration by storing them in a `.security.yml` file.

### Separation of Concerns

Configuration settings and secrets live in separate files, making the main config safe to share without exposing sensitive data.

### Easier Sharing

The main `config.json` can be shared or committed to version control. Only `.security.yml` must be kept private.

### Better Version Control

`.security.yml` should be added to `.gitignore` so secrets are never accidentally committed.

### Flexible Deployment

Different environments (dev, staging, production) can use different `.security.yml` files without touching `config.json`.

## File Structure

```
~/.omnipus/
├── config.json          # Main configuration (safe to share)
└── .security.yml         # Security data (never share)
```

## How It Works

The security configuration works through **direct field mapping**, NOT through `ref:` string references. The system automatically loads values from `.security.yml` and applies them to the corresponding fields in `config.json`.

Values in `.security.yml` are automatically mapped to corresponding fields in the config based on field names and structure, not on reference strings. If a value exists in `.security.yml`, it **overrides** the value in `config.json`. You can omit sensitive fields from `config.json` entirely (recommended).

## Security Configuration Structure

### Complete Example: .security.yml

```yaml
# Model API Keys
# All models MUST use `api_keys` (plural) array format
# Even a single key must be provided as an array with one element
model_list:
  gpt-5.4:
    api_keys:
      - "sk-proj-your-actual-openai-key-1"
      - "sk-proj-your-actual-openai-key-2"  # Optional: Multiple keys for failover
  claude-sonnet-4.6:
    api_keys:
      - "sk-ant-your-actual-anthropic-key"  # Single key in array format

# Channel Tokens
channels:
  telegram:
    token: "your-telegram-bot-token"
  feishu:
    app_secret: "your-feishu-app-secret"
    encrypt_key: "your-feishu-encrypt-key"
    verification_token: "your-feishu-verification-token"
  discord:
    token: "your-discord-bot-token"
  weixin:
    token: "your-weixin-token"
  qq:
    app_secret: "your-qq-app-secret"
  dingtalk:
    client_secret: "your-dingtalk-client-secret"
  slack:
    bot_token: "your-slack-bot-token"
    app_token: "your-slack-app-token"
  matrix:
    access_token: "your-matrix-access-token"
  line:
    channel_secret: "your-line-channel-secret"
    channel_access_token: "your-line-channel-access-token"
  onebot:
    access_token: "your-onebot-access-token"
  wecom:
    token: "your-wecom-token"
    encoding_aes_key: "your-wecom-encoding-aes-key"
  wecom_app:
    corp_secret: "your-wecom-app-corp-secret"
    token: "your-wecom-app-token"
    encoding_aes_key: "your-wecom-app-encoding-aes-key"
  wecom_aibot:
    secret: "your-wecom-aibot-secret"
    token: "your-wecom-aibot-token"
    encoding_aes_key: "your-wecom-aibot-encoding-aes-key"
  irc:
    password: "your-irc-password"
    nickserv_password: "your-irc-nickserv-password"
    sasl_password: "your-irc-sasl-password"

# Web Tool API Keys
web:
  brave:
    api_keys:
      - "BSAyour-brave-api-key-1"
      - "BSAyour-brave-api-key-2"  # Optional: Multiple keys for failover
  tavily:
    api_keys:
      - "tvly-your-tavily-api-key"  # Single key in array format
  perplexity:
    api_keys:
      - "pplx-your-perplexity-api-key"  # Single key in array format
  glm_search:
    api_key: "your-glm-search-api-key"  # GLMSearch uses single key format (not array)
  baidu_search:
    api_key: "your-baidu-search-api-key"

# Skills Registry Tokens
skills:
  github:
    token: "your-github-token"
  clawhub:
    auth_token: "your-clawhub-auth-token"
```

## Usage

### Step 1: Create .security.yml

Create or copy the security file:
```bash
cp security.example.yml ~/.omnipus/.security.yml
```

### Step 2: Fill in your actual values

Edit `~/.omnipus/.security.yml` and replace placeholder values with your actual API keys and tokens.

### Step 3: Set proper permissions

```bash
chmod 600 ~/.omnipus/.security.yml
```

### Step 4: Simplify config.json (Recommended)

You can now remove sensitive fields from `config.json` since they're loaded from `.security.yml`:

**Before:**
```json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1",
      "api_key": "sk-your-actual-api-key-here"
    }
  ],
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "1234567890:ABCdefGHIjklMNOpqrsTUVwxyz"
    }
  }
}
```

**After:**
```json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1"
      // api_key is now loaded from .security.yml
    }
  ],
  "channels": {
    "telegram": {
      "enabled": true,
      // token is now loaded from .security.yml
    }
  }
}
```

### Step 5: Verify

Restart Omnipus and verify it loads correctly:
```bash
omnipus --version
```

## Field Mapping Rules

### Models

**In .security.yml:**
```yaml
model_list:
  <model_name>:
    api_keys:
      - "key-1"
      - "key-2"
```

The `api_keys` array maps to the model's API keys. The `<model_name>` must match the `model_name` field in `config.json`. Indexed names (e.g., `"gpt-5.4:0"`) are supported — the system will also try the base name (`"gpt-5.4"`) if the indexed name is not found.

### Channels

Each channel maps its fields directly. Given the following `.security.yml` entries:

**In .security.yml:**
```yaml
channels:
  telegram:
    token: "value"
  feishu:
    app_secret: "value"
    encrypt_key: "value"
    verification_token: "value"
  discord:
    token: "value"
```

The mappings resolve as follows:

| `.security.yml` key | `config.json` field |
|---|---|
| `channels.telegram.token` | `config.channels.telegram.token` |
| `channels.feishu.app_secret` | `config.channels.feishu.app_secret` |
| `channels.feishu.encrypt_key` | `config.channels.feishu.encrypt_key` |
| `channels.feishu.verification_token` | `config.channels.feishu.verification_token` |
| `channels.discord.token` | `config.channels.discord.token` |

### Web Tools

#### Brave, Tavily, Perplexity

Use `api_keys` (plural) array format:

```yaml
web:
  brave:
    api_keys:
      - "key-1"
      - "key-2"
```

#### GLMSearch

Use `api_key` (singular) single string format:

```yaml
web:
  glm_search:
    api_key: "single-key-here"
```

#### BaiduSearch

Use `api_key` (singular) single string format:

```yaml
web:
  baidu_search:
    api_key: "your-key"
```

### Skills

**In .security.yml:**
```yaml
skills:
  github:
    token: "value"
  clawhub:
    auth_token: "value"
```

## API Key Formats

### Models — Single Key

Use array format with one element:
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-your-key"
```

### Models — Multiple Keys (Load Balancing and Failover)

Use array format with multiple elements:
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-your-key-1"
      - "sk-your-key-2"
      - "sk-your-key-3"
```

Multiple keys enable load balancing (requests distributed across keys), automatic failover when a key fails, rate limit management across multiple keys, and higher availability during API provider issues.

### Web Tools (Brave/Tavily/Perplexity) — Single Key

```yaml
web:
  brave:
    api_keys:
      - "BSA-your-key"
```

### Web Tools (Brave/Tavily/Perplexity) — Multiple Keys

```yaml
web:
  brave:
    api_keys:
      - "BSA-key-1"
      - "BSA-key-2"
```

### Web Tools (GLMSearch/BaiduSearch) — Single Key Only

```yaml
web:
  glm_search:
    api_key: "your-glm-key"  # Single string (NOT array)
  baidu_search:
    api_key: "your-baidu-key"  # Single string (NOT array)
```

## Model Name Matching

The system supports intelligent model name matching in `.security.yml`.

### Example 1: Exact Match

**config.json:**
```json
{
  "model_name": "gpt-5.4:0"
}
```

**.security.yml (exact match with index):**
```yaml
model_list:
  gpt-5.4:0:
    api_keys: ["key-1"]
```

### Example 2: Base Name Match

**config.json:**
```json
{
  "model_name": "gpt-5.4:0"
}
```

**.security.yml (base name without index):**
```yaml
model_list:
  gpt-5.4:
    api_keys: ["key-1", "key-2"]
```

Both methods work. The base name match allows you to use simpler keys in `.security.yml` even when your config uses indexed model names for load balancing.

## Backward Compatibility

The system maintains full backward compatibility. Direct values can still be used in `config.json` (not recommended for production). Mixed usage is supported — some fields in `.security.yml` and others in `config.json`. The security file is optional: if `.security.yml` doesn't exist, the system uses only values from `config.json`. When a field exists in both files, the `.security.yml` value takes precedence.

## Environment Variables

Environment variables have the highest priority and override both `config.json` and `.security.yml` values. The pattern is `OMNIPUS_<SECTION>_<KEY>_<FIELD>` with underscores separating path segments, converted to uppercase.

| Variable | Example value | Purpose |
|---|---|---|
| `OMNIPUS_CHANNELS_TELEGRAM_TOKEN` | `token-from-env` | Override Telegram token |
| `OMNIPUS_CHANNELS_FEISHU_APP_SECRET` | `secret-from-env` | Override Feishu app secret |
| `OMNIPUS_TOOLS_WEB_BRAVE_API_KEY` | `key-from-env` | Override Brave API key |
| `OMNIPUS_TOOLS_WEB_BAIDU_API_KEY` | `baidu-key-from-env` | Override Baidu API key |

## Security Best Practices

### Never Commit `.security.yml`

Add `.security.yml` to your `.gitignore` to prevent accidental commits.

### Set File Permissions

Run `chmod 600 ~/.omnipus/.security.yml` immediately after creating the file.

### Use Different Keys per Environment

Maintain separate API keys for dev, staging, and production. Each environment has its own `.security.yml`.

### Rotate Keys Regularly

Update `.security.yml` whenever you rotate API keys. Update all environments before revoking the old key.

### Back Up Securely

When backing up `.security.yml`, ensure the backup is also encrypted. Never store backups in plaintext alongside the codebase.

### Review Access

Ensure only authorized users have read access to the file. Check with `ls -l ~/.omnipus/.security.yml`.

## API

### loadSecurityConfig

```go
func loadSecurityConfig(securityPath string) (*SecurityConfig, error)
```

Loads the security configuration from `.security.yml`. Returns an empty `SecurityConfig` if the file doesn't exist.

### saveSecurityConfig

```go
func saveSecurityConfig(securityPath string, sec *SecurityConfig) error
```

Saves the security configuration to `.security.yml` with `0o600` permissions.

### applySecurityConfig

```go
func applySecurityConfig(cfg *Config, sec *SecurityConfig) error
```

Applies security configuration to the main config by copying values from `.security.yml` to the corresponding fields in the config.

### securityPath

```go
func securityPath(configPath string) string
```

Returns the path to `.security.yml` relative to the config file.

## Example: Complete Configuration

### config.json

```json
{
  "version": 1,
  "agents": {
    "defaults": {
      "workspace": "~/omnipus-workspace",
      "model_name": "gpt-5.4"
    }
  },
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1"
    },
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_base": "https://api.anthropic.com/v1"
    }
  ],
  "channels": {
    "telegram": {
      "enabled": true
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": true
      }
    }
  }
}
```

### .security.yml

```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-proj-actual-openai-key-1"
      - "sk-proj-actual-openai-key-2"
  claude-sonnet-4.6:
    api_keys:
      - "sk-ant-actual-anthropic-key"

channels:
  telegram:
    token: "1234567890:ABCdefGHIjklMNOpqrsTUVwxyz"

web:
  brave:
    api_keys:
      - "BSAactualbravekey-1"
      - "BSAactualbravekey-2"
  tavily:
    api_keys:
      - "tvly-your-tavily-key"
  glm_search:
    api_key: "your-glm-key"
  baidu_search:
    api_key: "your-baidu-key"
```

## Testing

Run the security configuration tests:

```bash
go test ./pkg/config -run TestSecurityConfig
```

## Troubleshooting

### Error: "failed to load security config"

Verify `.security.yml` exists in the same directory as `config.json`. Check that the YAML syntax is valid (use a YAML validator). Ensure file permissions allow reading.

### Error: "model security entry not found"

Ensure the model name in `config.json` matches exactly in `.security.yml` and that the `model_list` section exists in `.security.yml`. For models with indexed names (e.g., `"gpt-5.4:0"`), use the exact name or the base name without index. Verify the YAML structure is correct with proper indentation.

### Multiple API Keys Not Working

Ensure you're using `api_keys` (plural) in `.security.yml` for models and web tools — except GLMSearch and BaiduSearch which use `api_key` (singular). Check that the array format is correct in YAML with proper indentation and dashes. Models, Brave, Tavily, and Perplexity MUST use `api_keys` (array format). GLMSearch and BaiduSearch MUST use `api_key` (single string format).

### Load Balancing/Failover Issues

Verify all API keys in the `api_keys` array are valid and that all keys have the same rate limits and permissions. Monitor logs to see which keys are being used and failing. Ensure the `api_keys` array is properly formatted in YAML.

### Keys Not Being Applied

Check that `.security.yml` is in the same directory as `config.json` and that the file permissions allow reading (`chmod 600 ~/.omnipus/.security.yml`). Ensure the YAML structure matches the expected format. Check for typos in field names (case-sensitive). Verify the model and channel names match exactly (case-sensitive).

## Migration Guide

### Step 1: Backup your config

```bash
cp ~/.omnipus/config.json ~/.omnipus/config.json.backup
```

### Step 2: Create .security.yml

```bash
cp security.example.yml ~/.omnipus/.security.yml
```

### Step 3: Fill in your API keys

Edit `~/.omnipus/.security.yml` and replace placeholder values with your actual keys.

### Step 4: Remove sensitive fields from config.json

Remove or comment out the following from `config.json`: `api_key` fields from `model_list` entries, `token` fields from `channels`, `api_key` fields from `tools.web`, and `token`/`auth_token` fields from `tools.skills`.

### Step 5: Set proper permissions

```bash
chmod 600 ~/.omnipus/.security.yml
```

### Step 6: Test

```bash
omnipus --version
```

### Step 7: Verify functionality

Test your models and channels to ensure everything works correctly.

### Step 8: Clean up (optional)

If everything works, you can delete the backup:
```bash
rm ~/.omnipus/config.json.backup
```

## Advanced: Encrypted API Keys

Omnipus supports encrypting API keys in the security file for additional protection.

### Setup

Set a passphrase via environment variable:
```bash
export OMNIPUS_CREDENTIAL_PASSPHRASE="your-secure-passphrase"
```

When saving config, API keys will be encrypted automatically:
```go
SaveConfig(path, config)
```

### Encrypted Format

Encrypted keys are stored as:
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "enc://encrypted-base64-string"
```

The system automatically decrypts keys at runtime when loading the configuration.

### Additional Layer of Security

Keys are encrypted at rest and the passphrase can be managed separately from the config file, providing an additional layer of protection beyond file permissions.

### Important Notes

Always backup your passphrase securely. If you lose the passphrase, you'll lose access to encrypted keys. Use a strong, unique passphrase and never commit it to version control.
