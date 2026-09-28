// Package kafka holds the sarama client configuration shared by both services.
package kafka

import "github.com/IBM/sarama"

// ProducerConfig returns a producer configuration with idempotent writes
// acknowledged by all in-sync replicas, so retries can neither lose nor
// duplicate records.
func ProducerConfig(clientID string) *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.ClientID = clientID
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Idempotent = true
	cfg.Net.MaxOpenRequests = 1 // required by the idempotent producer
	cfg.Producer.Retry.Max = 5
	cfg.Producer.Return.Successes = true
	cfg.Producer.Return.Errors = true
	cfg.Producer.Compression = sarama.CompressionSnappy
	return cfg
}

// ConsumerConfig returns a consumer group configuration. Groups with no
// committed offset start from the oldest retained record.
func ConsumerConfig(clientID string) *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.ClientID = clientID
	cfg.Consumer.Return.Errors = true
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategySticky()}
	return cfg
}
