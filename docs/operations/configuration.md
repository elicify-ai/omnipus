# Configuration

This page is the operator entry point for file-level configuration. User-facing choices belong in the web app and are described in [Settings](../settings.md).

## Files and overrides

By default, Omnipus reads `config.json` from `$OMNIPUS_HOME`, which defaults to `~/.omnipus`. Set `OMNIPUS_HOME` to move the whole data directory, or pass `--config` to select a particular configuration file.

Environment variables can override supported fields. They use uppercase names with underscores, such as `OMNIPUS_GATEWAY_LOG_LEVEL`. Keep credentials out of `config.json`; store them through Settings or the credential commands so the file contains references instead of plaintext secrets.

The gateway listens on port `5000` by default. The web app, application programming interface, WebSocket connection, and preview routes share this listener. Set `gateway.public_url` when a reverse proxy exposes a different public origin.

## Configuration areas

| Area | Operator reference |
|---|---|
| Gateway and proxy | [Reverse proxy](reverse-proxy.md) |
| Process isolation | [Sandbox configuration](sandbox-config.md) and [sandbox limitations](sandbox-limitations.md) |
| Tool backends | [Tools configuration](tools-configuration.md) |
| Credentials and filtering | [Security considerations](security-considerations.md) |
| Schema upgrades | [Configuration versioning](config-versioning.md) and [model-list migration](../migration/model-list-migration.md) |

Provider and model choices change over time. Use the live Providers screen for the available catalog and [Providers and models](../providers-and-models.md) for the user-facing explanation rather than copying a provider list into configuration documentation.

Changes that affect the listening address, public origin, or boot-time sandbox require a gateway restart. The relevant specialist page identifies restart-gated fields.
