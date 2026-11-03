// Command worker consumes order events from Kafka.
package main

import (
	"context"

	"github.com/segmentio/kafka-go"

	"example.com/shop/internal/orders"
	"example.com/shop/internal/store"
)

func main() {
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"localhost:9092"}, Topic: "orders"})
	o := orders.New(store.Open("postgres://localhost/shop"))
	for {
		m, err := r.ReadMessage(context.Background())
		if err != nil {
			return
		}
		o.Fulfil(string(m.Key))
	}
}
