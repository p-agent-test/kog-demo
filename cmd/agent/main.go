package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/p-blackswan/platform-agent/internal/agent"
	"github.com/p-blackswan/platform-agent/internal/bridge"
	"github.com/p-blackswan/platform-agent/internal/config"
	ghclient "github.com/p-blackswan/platform-agent/internal/github"
	"github.com/p-blackswan/platform-agent/internal/health"
	jiraclient "github.com/p-blackswan/platform-agent/internal/jira"
	"github.com/p-blackswan/platform-agent/internal/mgmt"
	slackpkg "github.com/p-blackswan/platform-agent/internal/slack"
	"github.com/p-blackswan/platform-agent/internal/supervisor"
	"github.com/p-blackswan/platform-agent/pkg/tokenstore"
)

func main() {
	// Setup structured logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	logger := zerolog.New(os.Stdout).With().Timestamp().Caller().Logger()

	if os.Getenv("ENVIRONMENT") == "development" {
		logger = logger.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	log.Logger = logger

	// Load config
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load config")
	}

	// Set log level
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err == nil {
		zerolog.SetGlobalLevel(level)
	}

	logger.Info().
		Str("environment", cfg.Environment).
		Int("http_port", cfg.HTTPPort).
		Str("mgmt_addr", cfg.MgmtListenAddr).
		Bool("slack_enabled", cfg.SlackEnabled()).
		Msg("starting platform agent")

	// Context with graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Initialize token store
	store := tokenstore.NewMemoryStore()

	// Initialize supervisor
	policy := supervisor.DefaultPolicy()
	audit := supervisor.NewAuditLog(logger)
	sup := supervisor.NewSupervisor(policy, audit, cfg.TokenTTL, logger)

	// Health checker
	checker := health.NewChecker(logger)

	// Initialize GitHub client (if configured)
	var ghClient *ghclient.Client
	if cfg.GitHubEnabled() {
		var ghErr error
		ghClient, ghErr = ghclient.NewClient(
			cfg.GitHubAppID,
			cfg.GitHubInstallationID,
			cfg.GitHubPrivateKeyPath,
			store,
			logger,
		)
		if ghErr != nil {
			logger.Warn().Err(ghErr).Msg("failed to init GitHub client (non-fatal)")
		} else {
			logger.Info().Msg("GitHub App client initialized")
			checker.Register("github", func(ctx context.Context) health.Status {
				_, err := ghClient.GetInstallationClient(ctx)
				if err != nil {
					return health.StatusDown
				}
				return health.StatusOK
			})
		}
	} else {
		logger.Info().Msg("GitHub not configured — skipping")
	}

	// Initialize Jira client (if configured)
	var jiraClient *jiraclient.Client
	if cfg.JiraEnabled() {
		var auth jiraclient.Authenticator
		if cfg.JiraAPIEmail != "" && cfg.JiraAPIToken != "" {
			auth = &jiraclient.BasicAuth{
				Email:    cfg.JiraAPIEmail,
				APIToken: cfg.JiraAPIToken,
			}
		} else if cfg.JiraClientID != "" {
			auth = jiraclient.NewOAuthAuth("", nil)
		}

		if auth != nil {
			jiraClient = jiraclient.NewClient(cfg.JiraBaseURL, auth, logger)
			logger.Info().Msg("Jira client initialized")
			checker.Register("jira", func(ctx context.Context) health.Status {
				return health.StatusOK
			})
		}
	} else {
		logger.Info().Msg("Jira not configured — skipping")
	}

	// Initialize Agent (the task executor)
	agentInstance := agent.NewAgent(ghClient, jiraClient, sup, nil, audit, agent.Config{
		SupervisorChannel: cfg.SupervisorChannel,
		JiraProjectKey:    "PLAT", // default; could be in config
		DefaultNamespace:  "default",
		AllowedNamespaces: []string{},
	}, logger)

	// HTTP server for webhooks and health
	mux := http.NewServeMux()
	mux.HandleFunc("/health", health.LivenessHandler())
	mux.HandleFunc("/ready", checker.ReadinessHandler())

	// GitHub webhook
	ghWebhook := ghclient.NewWebhookHandler(cfg.GitHubWebhookSecret, logger)
	mux.Handle("/webhook/github", ghWebhook)

	// Jira webhook
	jiraWebhook := jiraclient.NewWebhookHandler(logger)
	mux.Handle("/webhook/jira", jiraWebhook)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// WaitGroup for in-flight work
	var wg sync.WaitGroup

	// --- Management API ---
	callbacks := mgmt.NewCallbackDelivery(cfg.CallbackTimeout, cfg.CallbackRetries, logger)

	// Wire Agent as the real TaskExecutor (replaces NoOpExecutor)
	taskEngine := mgmt.NewTaskEngine(mgmt.TaskEngineConfig{
		Workers:   cfg.MgmtWorkers,
		QueueSize: 1000,
	}, agentInstance, callbacks, logger)

	taskEngine.Start(ctx)

	// Wire task engine as requeuer so agent can re-queue tasks after approval
	agentInstance.SetRequeuer(taskEngine)

	rtCfg := &mgmt.RuntimeConfig{
		Environment:    cfg.Environment,
		LogLevel:       cfg.LogLevel,
		HTTPPort:       cfg.HTTPPort,
		MgmtListenAddr: cfg.MgmtListenAddr,
		RateLimitRPS:   cfg.MgmtRateLimitRPS,
		RateLimitBurst: cfg.MgmtRateLimitBurst,
		AuthMode:       cfg.MgmtAuthMode,
		WorkerCount:    cfg.MgmtWorkers,
	}

	mgmtServer := mgmt.NewServer(mgmt.ServerConfig{
		ListenAddr: cfg.MgmtListenAddr,
		AuthConfig: mgmt.AuthConfig{
			Mode:   cfg.MgmtAuthMode,
			APIKey: cfg.MgmtAPIKey,
		},
		RateLimit: mgmt.RateLimitConfig{
			RPS:   cfg.MgmtRateLimitRPS,
			Burst: cfg.MgmtRateLimitBurst,
		},
		CORSOrigins: cfg.MgmtCORSOrigins,
		TLSCert:     cfg.MgmtTLSCert,
		TLSKey:      cfg.MgmtTLSKey,
		Workers:     cfg.MgmtWorkers,
	}, taskEngine, checker, nil, rtCfg, logger)

	// Start HTTP server
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info().Int("port", cfg.HTTPPort).Msg("HTTP server starting")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	// Start Management API server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := mgmtServer.Start(); err != nil {
			logger.Error().Err(err).Msg("management API server error")
		}
	}()

	// Start Slack Socket Mode (optional — only if tokens provided)
	if cfg.SlackEnabled() {
		slackMiddleware := slackpkg.NewMiddleware(logger, 10, time.Minute)
		slackHandler := slackpkg.NewHandler(logger, slackMiddleware)
		slackApp, slackErr := slackpkg.NewApp(cfg.SlackBotToken, cfg.SlackAppToken, cfg.SlackAllowedChannelList(), logger, slackHandler)
		if slackErr != nil {
			logger.Error().Err(slackErr).Msg("failed to init Slack app (non-fatal)")
		} else {
			// Get bot user ID for self-message filtering
			botUserID := ""
			if authResp, authErr := slackApp.AuthTest(); authErr == nil {
				botUserID = authResp.UserID
				logger.Info().Str("bot_user_id", botUserID).Msg("Slack bot identity resolved")
			}

			// Initialize bridge (Slack → Kog-2 via openclaw CLI)
			slackBridge := bridge.New(bridge.Config{
				OpenClawBin:    cfg.OpenClawBin,
				GatewayURL:     cfg.OpenClawURL,
				GatewayToken:   cfg.OpenClawToken,
				BotUserID:      botUserID,
				MaxConcurrent:  5,
			}, bridge.NewSlackPoster(slackApp), logger)

			slackHandler.SetForwarder(slackBridge)

			// Wire Slack into Agent so approval buttons can be sent
			agentInstance.SetSlack(slackApp)
			// Wire Agent as approval handler so Slack buttons trigger re-queue
			slackHandler.SetApprovalHandler(agentInstance)

			logger.Info().Msg("Slack Socket Mode enabled (bridge + interactive callbacks)")
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := slackApp.Run(ctx); err != nil {
					logger.Error().Err(err).Msg("Slack Socket Mode error")
				}
			}()
		}
	} else {
		logger.Info().Msg("Slack not configured — running in API-only mode")
	}

	_ = store // suppress unused (used for GitHub token caching)

	// Wait for shutdown signal
	sig := <-sigCh
	logger.Info().Str("signal", sig.String()).Msg("shutting down gracefully")

	// Cancel context to signal all goroutines
	cancel()

	// Shutdown servers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}

	if err := mgmtServer.Shutdown(); err != nil {
		logger.Error().Err(err).Msg("management API server shutdown error")
	}

	taskEngine.Stop()

	// Wait for in-flight work to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info().Msg("all goroutines stopped")
	case <-time.After(15 * time.Second):
		logger.Warn().Msg("forced shutdown after timeout")
	}

	logger.Info().Msg("platform agent stopped")
}
