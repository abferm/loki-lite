package main

import (
	"fmt"
	"net/http"
)

func main() {
	fmt.Println("Loki Lite — a Loki-compatible query interface for journald logs")
	http.ListenAndServe(":3100", nil)
}
