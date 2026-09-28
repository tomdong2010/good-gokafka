# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [1.0.0] - 2026-09-28

First tagged release. The project was rewritten from a minimal demo into a
production-minded reference.

### Added
- HTTP producer with validation, request limits and meaningful status codes;
  records keyed by sender and tagged with a `content-type` header.
- Idempotent producer (`acks=all`), snappy compression.
- Consumer group with sticky rebalancing and at-least-once offset commits.
- Retry with exponential backoff and a dead-letter topic (`<topic>.dlq`) that
  records the original topic, partition, offset and error in headers.
- Protobuf and JSON codecs, selected per record.
- Prometheus metrics for both services and a provisioned Grafana dashboard.
- TLS, mutual TLS and SASL (PLAIN, SCRAM-SHA-256, SCRAM-SHA-512).
- Docker Compose demo: Kafka in KRaft mode, scalable consumers, optional
  Kafka UI and monitoring profiles; distroless multi-arch images.
- Unit tests with `-race`, integration tests against a real broker, CI,
  release automation (binaries for Linux, macOS and Windows; images on GHCR).
- English and Simplified Chinese documentation.

### Changed
- Single Go module (`github.com/tomdong2010/good-gokafka`), Go 1.24,
  `IBM/sarama` instead of the unmaintained `Shopify/sarama`.
- Configuration through environment variables; `KAFKA_ADDRESS` and
  `ZOOKEEPER_HOST` are still accepted as broker fallbacks.

### Fixed
- The project did not build: module paths did not match the imports.
- The consumer panicked on a protobuf record without content, and only ever
  read partition 0 from the oldest offset.

[1.0.0]: https://github.com/tomdong2010/good-gokafka/releases/tag/v1.0.0
