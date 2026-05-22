// Command qwen2api launches the OpenAI-compatible HTTP gateway for chat.qwen.ai.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/keaume34/qwen2api/internal/affinity"
	"github.com/keaume34/qwen2api/internal/config"
	"github.com/keaume34/qwen2api/internal/filecache"
	"github.com/keaume34/qwen2api/internal/metrics"
	"github.com/keaume34/qwen2api/internal/promptcache"
	"github.com/keaume34/qwen2api/internal/qwen"
	"github.com/keaume34/qwen2api/internal/reqlog"
	"github.com/keaume34/qwen2api/internal/server"
	"github.com/keaume34/qwen2api/internal/tokencount"
	"github.com/keaume34/qwen2api/internal/tokenpool"
	"github.com/keaume34/qwen2api/internal/tunnel"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qwen2api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	slog.SetDefault(logger)

	if len(cfg.Tokens) == 0 {
		logger.Warn("starting with empty token pool; chat completions will fail until tokens are configured")
	}
	if len(cfg.APIKeys) == 0 {
		logger.Warn("starting without QWEN2API_API_KEY; the server is UNAUTHENTICATED")
	}

	pool := tokenpool.New(cfg.Tokens, time.Duration(cfg.CooldownSeconds)*time.Second)
	client := qwen.NewClient(qwen.ClientConfig{
		BaseURL:                cfg.BaseURL,
		UserAgent:              cfg.UserAgent,
		SsxmodItna:             cfg.SsxmodItna,
		Ssxmodi2:               cfg.SsxmodItna2,
		TimeoutSeconds:         cfg.TimeoutSeconds,
		PoolingEnabled:         cfg.Features.ConnectionPooling,
		BrowserFallbackEnabled: cfg.Features.BrowserEngineFallback,
	})
	client.SetConfigRef(&cfg)

	var cache *promptcache.Cache
	if cfg.Features.PromptCaching {
		cache = promptcache.New(cfg.Cache.MaxEntries, time.Duration(cfg.Cache.TTLSeconds)*time.Second)
	}

	var metricsReg *metrics.Registry
	if cfg.Features.Metrics {
		metricsReg = metrics.New()
	}

	var reqLogger *reqlog.Logger
	if cfg.Features.RequestLogging {
		rl, err := reqlog.NewLogger(cfg.Logging.Path, cfg.Logging.MaxSizeMB, cfg.Logging.MaxBackups, cfg.Logging.TruncateLen)
		if err != nil {
			logger.Warn("request logging init failed", "err", err)
		} else {
			reqLogger = rl
		}
	}

	// tokenHealthLoop runs unconditionally but checks Features.AutoTokenRefresh dynamically.
	go tokenHealthLoop(logger, client, pool, &cfg)

	var affinityStore *affinity.Store
	if cfg.Features.SessionAffinity {
		affinityStore = affinity.NewStore(2 * time.Hour)
	}

	var fileCache *filecache.Cache
	if cfg.Features.FileCache {
		fileCache = filecache.New(200, 15*time.Minute)
	}

	var tokenCounter *tokencount.Counter
	tokenCounter = tokencount.New(4.0)

	var tunnelMgr *tunnel.Manager
	if cfg.Features.Tunnel {
		tunnelMgr = tunnel.New(logger)
	}

	srv := server.New(server.Deps{
		Config:        &cfg,
		Logger:        logger,
		Qwen:          client,
		TokenPool:     pool,
		Cache:         cache,
		Metrics:       metricsReg,
		ReqLog:        reqLogger,
		Affinity:      affinityStore,
		FileCache:     fileCache,
		TokenCounter:  tokenCounter,
		TunnelManager: tunnelMgr,
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("qwen2api listening", "addr", addr, "tokens", len(cfg.Tokens), "api_keys", len(cfg.APIKeys))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if reqLogger != nil {
		_ = reqLogger.Close()
	}
	if tunnelMgr != nil {
		_ = tunnelMgr.Stop()
	}
	return nil
}

// tokenHealthLoop periodically pings upstream to detect dead/expired tokens
// and decodes JWT exp to warn before expiry.
func tokenHealthLoop(logger *slog.Logger, client *qwen.Client, pool *tokenpool.Pool, cfg *config.Config) {
	interval := time.Duration(cfg.TokenRefresh.CheckIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	warnBefore := time.Duration(cfg.TokenRefresh.WarnBeforeSeconds) * time.Second
	if warnBefore <= 0 {
		warnBefore = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !cfg.Features.AutoTokenRefresh {
			continue
		}
		now := time.Now()
		for _, st := range pool.Statuses() {
			exp := decodeJWTExp(st.Value)
			if exp > 0 {
				remaining := time.Until(time.Unix(exp, 0))
				if remaining <= 0 {
					logger.Warn("token expired", "name", st.Name)
					pool.MarkBad(st.Value)
					continue
				}
				if remaining < warnBefore {
					logger.Warn("token expiring soon", "name", st.Name, "remaining_seconds", int(remaining.Seconds()))
				}
			}
			if st.OnCooldown {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := client.Models(ctx, st.Value)
			cancel()
			if err != nil {
				logger.Warn("token health probe failed", "name", st.Name, "err", err)
			}
			_ = now
		}
	}
}
