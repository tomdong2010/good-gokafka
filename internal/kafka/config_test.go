package kafka

import "testing"

func TestConfigsAreValid(t *testing.T) {
	if err := ProducerConfig("test").Validate(); err != nil {
		t.Errorf("producer config: %v", err)
	}
	if err := ConsumerConfig("test").Validate(); err != nil {
		t.Errorf("consumer config: %v", err)
	}
}
