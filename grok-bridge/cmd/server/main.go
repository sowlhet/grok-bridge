package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/wlhet/grok-bridge/internal/api"
	"github.com/wlhet/grok-bridge/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config YAML")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cfg.ApplyEnv()

	s := api.NewServer(api.ServerDeps{})
	log.Printf("listening on %s", cfg.Server.Listen)
	log.Fatal(http.ListenAndServe(cfg.Server.Listen, s.Handler()))
}
