# Contributing

Thanks for helping out! Bug reports, docs fixes and new Kafka patterns are all welcome.

## Development

Requirements: Go (version in [`go.mod`](go.mod)), Docker, and optionally
[golangci-lint](https://golangci-lint.run/) v2.

```shell
make test          # unit tests with -race
make lint          # golangci-lint
make integration   # starts Kafka in Docker and runs tests against it
make demo          # full stack: Kafka + producer + 3 consumers
```

Run `make proto` after changing `internal/message/pb/message.proto`. Never renumber or reuse
field numbers, because records already in Kafka depend on them.

## Pull requests

- Keep each PR focused on one change, and include a test for new behaviour.
- `make lint test` should pass. CI also runs the integration tests and builds the Docker images.
- If you change behaviour or configuration, update both `README.md` and `README.zh-CN.md`.

## Releasing

Update `CHANGELOG.md`, then push a tag from `master`:

```shell
git tag v1.2.3 && git push origin v1.2.3
```

The release workflow publishes binaries for Linux, macOS and Windows, and multi-arch images to
`ghcr.io/tomdong2010/good-gokafka-{producer,consumer}`.
