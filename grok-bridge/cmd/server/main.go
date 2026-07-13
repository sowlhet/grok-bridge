package main

import (
	"log"
	"net/http"
	"os"

	"github.com/wlhet/grok-bridge/internal/api"
)

func main() {
	addr := ":8080"
	if v := os.Getenv("GROK_BRIDGE_LISTEN"); v != "" {
		addr = v
	}
	s := api.NewServer(api.ServerDeps{})
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, s.Handler()))
}
