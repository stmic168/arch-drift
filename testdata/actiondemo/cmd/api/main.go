// Command api serves the public HTTP API.
package main

import (
	"fmt"
	"net/http"

	"example.com/shop/internal/catalog"
	"example.com/shop/internal/orders"
	"example.com/shop/internal/store" // planted FORBIDDEN_EDGE: the API must not touch the store directly
	"example.com/shop/pkg/metrics"
	"github.com/segmentio/kafka-go"
)

func main() {
	db := store.Open("postgres://localhost/shop")
	o := orders.New(db)
	c := catalog.New()
	metrics.Register("api")
	events := &kafka.Writer{Addr: kafka.TCP("localhost:9092"), Topic: "orders"} // order events, consumed by the Worker
	defer events.Close()
	http.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, o.Count()) })
	http.HandleFunc("/products", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, c.List()) })
	_ = http.ListenAndServe(":8080", nil)
}
