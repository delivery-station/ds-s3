# ds-s3

`ds-s3` is a Delivery Station plugin that publishes build artifacts to S3-compatible object storage. It can be executed directly from DS workflows (`ds s3 upload …`) or installed as part of a finalization pipeline.

## Features

- Upload files or entire directories to any AWS S3 or S3-compatible provider
- Configurable context path prefixes for uploaded objects
- Optional cleanup step that removes existing objects before upload
- Overwrite control with safe defaults (enabled by default, configurable via DS config)
- Custom endpoints with optional TLS verification skips for on-prem providers (off by default)
- Credentials resolution through the AWS SDK default chain with optional static access keys from DS config
- Path-style addressing for providers that require it (e.g. MinIO)

## Configuration

Configure the plugin through the DS configuration file (`ds.yaml`). All settings live under `plugins.settings.s3` and can be overridden via CLI flags on demand.

```yaml
plugins:
  settings:
    s3:
      bucket: "artifacts"
      region: "us-east-1"
      context_path: "builds/my-service"
      cleanup: true           # remove existing objects under context path before upload
      overwrite: true         # allow overwriting of conflicting objects (default true)
      endpoint: "https://minio.internal"  # optional custom endpoint
      force_path_style: true  # required by some S3-compatible services
      tls:
        skip_verify: false    # set true only when using self-signed certs
      profile: "ci-bot"       # optional shared credentials profile
      credentials:
        access_key_id: "AKIA..."         # optional static keys
        secret_access_key: "secret"
        session_token: ""               # optional session token
```

## Usage

```bash
ds s3 upload ./dist --context latest --cleanup
```

CLI flags override configuration values:

- `--bucket` – override target bucket
- `--context` – prefix for uploaded objects
- `--cleanup` – enable cleanup regardless of configuration
- `--overwrite=false` – disable overwriting existing objects
- `--endpoint` – use a custom S3-compatible endpoint
- `--force-path-style` – toggle path-style addressing
- `--skip-tls-verify` – disable TLS verification (requires `--endpoint`)
- `--profile` – select a shared credentials profile

## Development

```bash
make build     # build local binary
make build-all # build for all supported platforms
make test      # run unit tests
make tidy      # tidy go.mod
```

The module depends on the local `../ds` workspace via a Go `replace` directive to simplify development.
