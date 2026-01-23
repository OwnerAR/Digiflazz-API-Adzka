package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "digiflazz-api/internal/config"
    "digiflazz-api/internal/httpserver"
    "digiflazz-api/internal/logging"
)

func main() {
    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("config error: %v", err)
    }

    logger := logging.NewLogger(cfg.Env)
    logger.Infof("configuration: env=%s mockup_mode=%v", cfg.Env, cfg.MockupMode)

    srv := httpserver.NewServer(cfg, logger)

    go func() {
        if err := srv.Start(); err != nil && err != http.ErrServerClosed {
            logger.Errorf("server start error: %v", err)
        }
    }()

    logger.Infof("server listening on :%s", cfg.ServerPort)

    // graceful shutdown
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if err := srv.Shutdown(ctx); err != nil {
        logger.Errorf("server shutdown error: %v", err)
    }
}


