## Produce and consume messages with Go and Apache Kafka

- **producer**: HTTP API that validates a message, encodes it (protobuf or JSON) and publishes it to Kafka.
- **consumer**: joins a consumer group, decodes each record and logs it.

### Layout

```
internal/message   shared Message type, validation, proto/JSON codecs
internal/message/pb  generated protobuf code (make proto)
internal/config    environment / .env loading
producer/          HTTP server (handler) + Kafka publisher (pub)
consumer/          consumer group (sub) + message handler (handler)
```

### Getting started

Requires Go 1.24+ and Docker.

1. Start Kafka (single node, KRaft mode, no ZooKeeper):

   ```shell
   make up        # docker compose up -d
   ```

2. Start the producer and consumer (each reads the `.env` in its own directory):

   ```shell
   make build
   (cd producer && ../bin/producer)
   (cd consumer && ../bin/consumer)
   ```

3. Send a message:

   ```shell
   curl -X POST http://localhost:3000/api/send \
     -H 'content-type: application/json' \
     -d '{"from": "Wuriyanto", "content": {"header": "This is Message 2", "body": "Hello Kafka"}}'
   # {"message":"message sent","partition":0,"offset":0}
   ```

   `from` and `content.body` are required. Invalid input gets `400`/`422`, a body over 1 MiB gets `413`,
   and a broker failure gets `502`. `GET /healthz` is a liveness probe.

Run the tests with `make test`.

### Configuration

Values come from the environment; a `.env` file in the working directory is loaded if present,
and variables already set in the environment win.

| Variable | Service | Default | Description |
|---|---|---|---|
| `KAFKA_BROKERS` | both | – | Comma separated bootstrap servers. `KAFKA_ADDRESS` (producer) and `ZOOKEEPER_HOST` (consumer) are still accepted. |
| `KAFKA_TOPIC` | both | – | Topic to publish to; the consumer accepts a comma separated list. |
| `MESSAGE_FORMAT` | both | `proto` | `proto` or `json`. The consumer uses it only for records without a `content-type` header. |
| `KAFKA_CLIENT_ID` | both | `good-gokafka-producer` / `-consumer` | Kafka client ID. |
| `HTTP_ADDR` | producer | `:3000` | HTTP listen address. |
| `SHUTDOWN_TIMEOUT` | producer | `10s` | Time allowed for in-flight requests on shutdown. |
| `KAFKA_GROUP_ID` | consumer | `good-gokafka-consumer` | Consumer group ID. |

### Delivery semantics

- The producer is **idempotent** with `acks=all`, so retries neither lose nor duplicate records.
  Records are keyed by `from`, so messages from one sender stay in order on one partition.
  Each record carries a `content-type` header naming its encoding.
- The consumer is a **consumer group**: run more instances with the same `KAFKA_GROUP_ID` to split
  partitions between them. Offsets are committed after a record is handled (at-least-once), so a
  restart resumes where it stopped. A record that cannot be decoded is logged and skipped rather than
  blocking its partition.
- Both services shut down cleanly on `SIGINT`/`SIGTERM`: the producer drains HTTP requests and flushes,
  the consumer leaves the group and commits its offsets.
