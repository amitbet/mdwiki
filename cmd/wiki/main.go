package main

import (
	"context"
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
	var redisSvc *redisx.PubSub
	if r, err := redisx.New(redisx.Options{
		Enabled:     cfg.RedisEnabled,
		URL:         cfg.RedisURL,
		Addrs:       cfg.RedisAddrs,
		ClusterMode: cfg.RedisClusterMode,
		Username:    cfg.RedisUsername,
		Password:    cfg.RedisPassword,
	}); err != nil {
		log.Printf("redis: %v", err)
	} else if r != nil {
		redisSvc = r
		redisPub = r
		log.Println("redis enabled for cross-node Yjs sync and distributed git write queue")
	}
	hub := wshub.NewHub(redisPub)
	go hub.Run()

	sess := session.NewStore()
	srv := api.New(cfg, reg, store, sess, hub, redisSvc)
	if err := srv.BootstrapRootRepo(context.Background()); err != nil {
		log.Fatalf("bootstrap root repo: %v", err)
	}
	log.Printf("mdwiki listening %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, srv.Router()))
}
