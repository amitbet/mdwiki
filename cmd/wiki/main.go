package main

import (
	"log"
	"net/http"
	"os"

	"mdwiki/internal/api"
	"mdwiki/internal/appsettings"
	"mdwiki/internal/config"
	"mdwiki/internal/redisx"
	"mdwiki/internal/session"
	"mdwiki/internal/space"
	wshub "mdwiki/internal/ws"
)

func main() {
	cfg := config.FromEnv()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	reg, err := space.LoadRegistry(cfg.RegistryPath)
	if err != nil {
		reg = &space.Registry{}
	}
	store := appsettings.NewLocalFileStore(cfg.SettingsPath)

	var redisPub wshub.RedisPubSub
	if r, err := redisx.New(os.Getenv("MDWIKI_REDIS_URL")); err != nil {
		log.Printf("redis: %v", err)
	} else if r != nil {
		redisPub = r
		log.Println("redis pub/sub enabled for Yjs fan-out (publish path)")
	}
	hub := wshub.NewHub(redisPub)
	go hub.Run()

	sess := session.NewStore()
	srv := api.New(cfg, reg, store, sess, hub)
	log.Printf("mdwiki listening %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, srv.Router()))
}
