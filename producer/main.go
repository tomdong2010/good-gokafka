package main

import (
	"fmt"
	"net/http"
	"os"

	configEnv "github.com/joho/godotenv"
	"github.com/tomdong2010/good-gokafka/producer/src/handler"
	"github.com/tomdong2010/good-gokafka/producer/src/pub"
)

func main() {
	fmt.Println("Hello Kafka")

	err := configEnv.Load(".env")

	if err != nil {
		fmt.Println(".env is not loaded properly")
		os.Exit(2)
	}

	kafkaAddress, ok := os.LookupEnv("KAFKA_ADDRESS")

	if !ok {
		fmt.Println("cannot load KAFKA_ADDRESS from environment")
		os.Exit(2)
	}

	topic, ok := os.LookupEnv("KAFKA_TOPIC")

	if !ok {
		fmt.Println("cannot load KAFKA_TOPIC from environment")
		os.Exit(2)
	}

	publisher, err := pub.NewPublisher(kafkaAddress)
	if err != nil {
		fmt.Printf("Error create publisher %v", err.Error())
		os.Exit(2)
	}

	publisherHandler := handler.NewHTTPHandler(topic, publisher)

	http.Handle("/api/send", publisherHandler.PublishMessages())
	http.ListenAndServe(":3000", nil)

}
