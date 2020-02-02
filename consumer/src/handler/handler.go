package handler

import (
	"github.com/tomdong2010/good-gokafka/consumer/src/sub"
)

//WorkerHandler struct
type WorkerHandler struct {
	topic      string
	subscriber sub.Subscriber
}

//NewWorkerHandler WorkerHandler's constructor
func NewWorkerHandler(topic string, subscriber sub.Subscriber) *WorkerHandler {
	return &WorkerHandler{topic: topic, subscriber: subscriber}
}

//Pool function
func (h *WorkerHandler) Pool() {
	h.subscriber.Subscribe(h.topic)
}
