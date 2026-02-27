package main

import (
	"log"
	"net/http"
	"time"
)

func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received %s request from %s to %s",
		r.Method,
		r.RemoteAddr,
		r.URL.Path,
	)

	time.Sleep(2 * time.Second)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

func main() {
	http.HandleFunc("/", handler)

	log.Println("Server starting on :8000")
	err := http.ListenAndServe(":8000", nil)
	if err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
