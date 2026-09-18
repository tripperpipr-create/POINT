package main

import (
	"fmt"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "service is running") })
	_ = http.ListenAndServe(":8090", mux)
}
