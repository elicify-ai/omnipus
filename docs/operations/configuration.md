# Configuration

This page is the operator entry point for file-level configuration. User-facing choices belong in the web app and are described in [Settings](../settings.md).

## Files and overrides

By default, Omnipus reads `config.json` from `$OMNIPUS_HOME`, which defaults to `~/.omnipus`. Set `OMNIPUS_HOME` to move the whole data directory, or pass `--config` to select a particular configuration file.

Environment variables can override supported fields. They use uppercase names with underscores, such as `OMNIPUS_GATEWAY_LOG_LEVEL`. One former override is retired: `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` is copied into `config.json` once, on the first start after upgrading, and ignored after that. The limit it set, `agents.defaults.max_tool_iterations`, is changed in Settings, Performance; see [tool calls per turn](../settings.md#tool-calls-per-turn). Keep credentials out of `config.json`; store them through Settings or the credential commands so the file contains references instead of plaintext secrets.

The gateway listens on port `5000` by default. The web app, application programming interface, WebSocket connection, and preview routes share this listener. Set `gateway.public_url` when a reverse proxy exposes a different public origin.

## Configuration areas

| Area | Operator reference |
|---|---|
| Gateway and proxy | [Reverse proxy](reverse-proxy.md) |
| Process isolation | [Sandbox configuration](sandbox-config.md) and [sandbox limitations](sandbox-limitations.md) |
| Tool backends | [Tools configuration](tools-configuration.md) |
| Credentials and filtering | [Security considerations](security-considerations.md) |
| Provider and model format | [Current provider and model configuration](../migration/model-list-migration.md) |
| Configuration versions | [Configuration versioning](config-versioning.md) |

Provider and model choices change over time. Use the live Providers screen for the available catalog and [Providers and models](../providers-and-models.md) for the user-facing explanation rather than copying a provider list into configuration documentation.

Changes that affect the listening address, public origin, or boot-time sandbox require a gateway restart. The relevant specialist page identifies restart-gated fields.

## PDF delivery limits (`pdf_limits`)

The model catalogue answers only whether an exact provider/model entry lists the `pdf` input modality. A missing entry is not PDF-capable. All inline PDF limits, including whether to use the inline route, come from engine configuration. Catalogue refreshes cannot change them. Setting `inline_pdf: true` does not give a model a capability its catalogue entry lacks.

Omnipus embeds one [shipped PDF limits table](../../pkg/config/data/pdf_limits.yaml), with a default row and researched provider/model overrides. The default allows up to **50,000,000 decoded bytes per PDF**, **50,000,000 decoded bytes in total per request**, **100 pages per PDF**, and **5 PDFs per request**. There is no default whole-request byte ceiling. Provider/model overrides can differ in either direction; for example Gemini allows more pages, while Bedrock has a lower file-size limit. Comments in the shipped table mark assumptions with `[ASSUMED]` and explain conditional ceilings.

Add a `pdf_limits` section to the configuration file to change these values. It is a sparse overlay, not a replacement table:

```json
{
  "pdf_limits": {
    "default": {
      "max_documents_per_request": 3
    },
    "overrides": [
      {
        "provider": "amazon-bedrock",
        "max_file_bytes": 4000000
      },
      {
        "provider": "openrouter",
        "model": "anthropic/*",
        "max_pages_per_request": 80
      },
      {
        "provider": "openrouter",
        "model": "anthropic/claude-opus-5.5",
        "max_pages_per_request": 60
      }
    ]
  }
}
```

`provider` is an exact, case-sensitive catalogue provider id. Omit `model` (or use an empty string) for a provider-wide row. A model selector is either the exact, case-sensitive model id, including any slash in a gateway's id, or a non-empty prefix followed by one trailing `*`. For example `anthropic/*` matches that family; `*`, `a*b`, `?` and bracket patterns are not supported. Provider wildcards are not supported.

Resolution is **per field**, in this order:

1. Exact model id.
2. Matching model prefixes, longest first, then shorter matching prefixes.
3. Provider-wide row.
4. Default row.

At each identical `(provider, model)` key, a field stated in the operator file wins over the shipped field. A missing operator field leaves the shipped field at that key intact. A missing field in the merged row continues to the next less-specific matching row. Thus a partial exact-model row can inherit the family's other limits. A provider row does not replace a more-specific model setting, and changing the default does not replace a field stated by an override.

### Fields and units

All eight fields are optional at every level. A **positive integer is an inclusive limit** (equal is allowed); **`0` explicitly means no limit** and stops inheritance for that field. If no row states a numeric field, Omnipus does not enforce that dimension. `inline_pdf: false` selects extracted text instead of native PDF delivery; absent everywhere means true. JSON `null` is treated as not stated.

| Field | Meaning |
|---|---|
| `inline_pdf` | Whether inline PDF delivery is enabled, when the catalogue also lists PDF input |
| `max_file_bytes` | Decoded bytes in one PDF |
| `max_total_file_bytes` | Sum of decoded PDF bytes in one request |
| `max_request_bytes` | Whole serialized request body: text, encoded media, tool definitions and JSON |
| `max_pages_per_file` | Known pages in one PDF |
| `max_pages_per_request` | Sum of known PDF pages in one request |
| `max_documents_per_message` | PDFs in one **wire message**; a transport can group several tool results into one message |
| `max_documents_per_request` | PDFs in the request |

Byte values are literal bytes: 1 MB is 1,000,000 bytes and 1 MiB is 1,048,576 bytes. The separate media-size setting and file-reader limits still apply. Unknown page counts contribute zero pages, but still consume bytes and document counts. Newest PDFs are admitted first; files that do not fit are presented as text with a note. See [PDF delivery behavior](security-considerations.md#pdfs-sent-to-the-model-as-documents).

### Neutralizing or restoring a shipped override

There is no delete marker. Deleting an operator row restores the shipped row; `overrides: []` also leaves shipped overrides active. To neutralize a shipped override, use the **same key** and explicitly state the desired values. For example this removes the inline-route restriction and every client-side numeric ceiling for the OpenRouter Anthropic family:

```json
{
  "pdf_limits": {
    "overrides": [
      {
        "provider": "openrouter",
        "model": "anthropic/*",
        "inline_pdf": true,
        "max_file_bytes": 0,
        "max_total_file_bytes": 0,
        "max_request_bytes": 0,
        "max_pages_per_file": 0,
        "max_pages_per_request": 0,
        "max_documents_per_message": 0,
        "max_documents_per_request": 0
      }
    ]
  }
}
```

To use particular default-sized limits instead, explicitly put those desired values in the row rather than `0`. More-specific matching model rows still take precedence. The provider may reject a PDF even when Omnipus has no client-side ceiling.

Explicit values are used instead of deletion because they preserve the distinction between “inherit” and “unlimited,” survive save/reload unchanged, and keep operator intent clear when shipped research is updated. Omnipus saves only the operator overlay, not a copy of the shipped table. An absent section uses the current shipped table on both existing and fresh installs.

Unknown field names within `pdf_limits`, negative limits, wrong value types, empty provider ids, duplicate `(provider, model)` keys and invalid patterns reject configuration loading with the field or row index in the error. Other configuration sections retain their existing lenient loading behavior. Save valid JSON, then reload the configuration or restart the gateway; each PDF attempt reads the live configuration, not a catalogue snapshot of the limits.
