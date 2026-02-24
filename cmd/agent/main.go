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

	"encoding/json"

	"github.com/p-blackswan/platform-agent/internal/agent"
	"github.com/p-blackswan/platform-agent/internal/bridge"
	"github.com/p-blackswan/platform-agent/internal/config"
	ghclient "github.com/p-blackswan/platform-agent/internal/github"
	"github.com/p-blackswan/platform-agent/internal/health"
	jiraclient "github.com/p-blackswan/platform-agent/internal/jira"
	"github.com/p-blackswan/platform-agent/internal/mgmt"
	"github.com/p-blackswan/platform-agent/internal/project"
	slackpkg "github.com/p-blackswan/platform-agent/internal/slack"
	datastore "github.com/p-blackswan/platform-agent/internal/store"
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
	if cfg.SupervisorAutoApprove {
		policy.SetAllAutoApprove()
		logger.Warn().Msg("⚠️ SUPERVISOR_AUTO_APPROVE=true — all permissions auto-approved (dev/test only)")
	}
	audit := supervisor.NewAuditLog(logger)
	sup := supervisor.NewSupervisor(policy, audit, cfg.TokenTTL, logger)

	// Health checker
	checker := health.NewChecker(logger)

	// Initialize SQLite store
	dataStore, err := datastore.New(cfg.AgentDBPath, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to init SQLite store")
	}
	defer dataStore.Close()

	// Startup recovery
	failedCount, _ := dataStore.FailStuckTasks()
	if failedCount > 0 {
		logger.Info().Int64("count", failedCount).Msg("recovered stuck tasks (marked failed)")
	}

	// Start retention goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := dataStore.RunRetention(ctx); err != nil {
					logger.Warn().Err(err).Msg("retention cleanup error")
				}
			}
		}
	}()

	// Register store health check
	checker.Register("sqlite", func(ctx context.Context) health.Status {
		size, err := dataStore.DBSizeBytes()
		if err != nil {
			return health.StatusDown
		}
		if size > 50*1024*1024 {
			logger.Warn().Int64("size_bytes", size).Msg("database size warning")
		}
		return health.StatusOK
	})

	// Initialize GitHub multi-org client (if configured)
	var ghMulti *ghclient.MultiClient
	if cfg.GitHubEnabled() {
		orgs, orgErr := cfg.ParseGitHubOrgs()
		if orgErr != nil {
			logger.Warn().Err(orgErr).Msg("failed to parse GitHub orgs (non-fatal)")
		} else {
			// Convert config orgs to github orgs
			ghOrgs := make([]ghclient.OrgInstallation, len(orgs))
			for i, o := range orgs {
				ghOrgs[i] = ghclient.OrgInstallation{Owner: o.Owner, InstallationID: o.InstallationID}
			}
			var ghErr error
			ghMulti, ghErr = ghclient.NewMultiClient(
				cfg.GitHubAppID,
				cfg.GitHubPrivateKeyPath,
				ghOrgs,
				store,
				logger,
			)
			if ghErr != nil {
				logger.Warn().Err(ghErr).Msg("failed to init GitHub multi-client (non-fatal)")
			} else {
				logger.Info().
					Strs("orgs", ghMulti.Owners()).
					Msg("GitHub App multi-org client initialized")
				checker.Register("github", func(ctx context.Context) health.Status {
					defClient, err := ghMulti.Default()
					if err != nil {
						return health.StatusDown
					}
					_, err = defClient.GetInstallationClient(ctx)
					if err != nil {
						return health.StatusDown
					}
					return health.StatusOK
				})
			}
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
	agentInstance := agent.NewAgent(ghMulti, jiraClient, sup, nil, audit, agent.Config{
		SupervisorChannel: cfg.SupervisorChannel,
		JiraProjectKey:    "PLAT", // default; could be in config
		DefaultNamespace:  "default",
		AllowedNamespaces: []string{},
	}, logger)
	agentInstance.SetStore(dataStore)

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
	taskEngine.SetStore(dataStore)

	taskEngine.Start(ctx)

	// Wire task engine as requeuer so agent can re-queue tasks after approval
	agentInstance.SetRequeuer(taskEngine)
	// Wire agent as completion notifier so task results post to Slack
	taskEngine.SetNotifier(agentInstance)

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

	// Wire store to session context store
	mgmtServer.SetSessionContextStore(dataStore)

	// Initialize project subsystem
	projectStore := project.NewStore(dataStore, logger)
	projectManager := project.NewManager(projectStore, logger)

	// Register project API routes
	projectHandlers := mgmt.NewProjectHandlers(projectStore, projectManager, logger)
	projectHandlers.RegisterRoutes(mgmtServer.V1Group())

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

			// Initialize bridge (Slack → Kog-2)
			var slackBridge slackpkg.MessageForwarder
			if cfg.WSBridgeEnabled() {
				// WS bridge — persistent WebSocket to gateway
				privKeyPEM, readErr := os.ReadFile(cfg.WSPrivateKeyPath)
				if readErr != nil {
					logger.Fatal().Err(readErr).Str("path", cfg.WSPrivateKeyPath).Msg("failed to read WS private key")
				}

				// Auto-detect publicKey from paired.json if not explicitly set
				pubKey := cfg.WSPublicKey
				if pubKey == "" {
					pubKey = autoDetectPublicKey(cfg.WSDeviceID, logger)
				}

				// Ensure gateway URL has /ws/gateway path
				gwURL := cfg.OpenClawURL
				if gwURL == "" {
					gwURL = "ws://localhost:18789/ws/gateway"
				}

				wsCfg := bridge.WSConfig{
					GatewayURL:    gwURL,
					Token:         cfg.OpenClawToken,
					DeviceID:      cfg.WSDeviceID,
					PublicKey:     pubKey,
					PrivateKeyPEM: string(privKeyPEM),
					ClientID:      "gateway-client",
					Scopes:        []string{"operator.admin", "operator.approvals", "operator.pairing", "operator.write", "operator.read"},
				}

				wsClient := bridge.NewWSClient(wsCfg, logger)
				if connErr := wsClient.Connect(ctx); connErr != nil {
					logger.Warn().Err(connErr).Msg("⚠️ WS bridge connect failed — falling back to CLI bridge")
					// Fallback to CLI bridge
					slackBridge = bridge.New(bridge.Config{
						OpenClawBin:   cfg.OpenClawBin,
						GatewayURL:    cfg.OpenClawURL,
						GatewayToken:  cfg.OpenClawToken,
						BotUserID:     botUserID,
						MaxConcurrent: 5,
					}, bridge.NewSlackPoster(slackApp), logger)
					logger.Info().Msg("📟 CLI bridge active (fallback from WS failure)")
				} else {
					slackBridge = bridge.NewWSBridge(wsClient, bridge.NewSlackPoster(slackApp), botUserID, logger)
					logger.Info().Msg("🔌 WS bridge active (persistent WebSocket)")
				}
			} else {
				// CLI bridge — openclaw agent CLI
				slackBridge = bridge.New(bridge.Config{
					OpenClawBin:    cfg.OpenClawBin,
					GatewayURL:     cfg.OpenClawURL,
					GatewayToken:   cfg.OpenClawToken,
					BotUserID:      botUserID,
					MaxConcurrent:  5,
				}, bridge.NewSlackPoster(slackApp), logger)
				logger.Info().Msg("📟 CLI bridge active (openclaw agent)")
			}

			// Wrap bridge with project router
			var finalForwarder slackpkg.MessageForwarder
			if safBridge, ok := slackBridge.(interface {
				HandleMessageWithSession(ctx context.Context, channelID, userID, text, threadTS, messageTS, sessionKey string)
				HandleMessage(ctx context.Context, channelID, userID, text, threadTS, messageTS string)
				IsActiveThread(channelID, threadTS string) bool
			}); ok {
				projectRouter := project.NewRouter(projectStore, projectManager, safBridge, bridge.NewSlackPoster(slackApp), botUserID, logger)
				finalForwarder = projectRouter
			} else {
				finalForwarder = slackBridge
			}
			slackHandler.SetForwarder(finalForwarder)

			// Wire thread persistence from SQLite for restart recovery
			if dataStore != nil {
				threadLookup := bridge.ThreadLookup(func(channel, threadTS string) bool {
					ts, err := dataStore.GetThreadSession(channel, threadTS)
					return err == nil && ts != nil
				})
				threadSaver := bridge.ThreadSaver(func(channel, threadTS, sessionKey string) {
					now := time.Now().UnixMilli()
					_ = dataStore.SaveThreadSession(&datastore.ThreadSession{
						Channel:       channel,
						ThreadTS:      threadTS,
						SessionKey:    sessionKey,
						CreatedAt:     now,
						LastMessageAt: now,
					})
				})
				if b, ok := slackBridge.(*bridge.WSBridge); ok {
					b.SetThreadLookup(threadLookup)
					b.SetThreadSaver(threadSaver)
				} else if b, ok := slackBridge.(*bridge.Bridge); ok {
					b.SetThreadLookup(threadLookup)
					b.SetThreadSaver(threadSaver)
				}
			}

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

// autoDetectPublicKey reads the device's public key from OpenClaw's paired.json.
func autoDetectPublicKey(deviceID string, logger zerolog.Logger) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(home + "/.openclaw/devices/paired.json")
	if err != nil {
		logger.Debug().Err(err).Msg("no paired.json found — WS_PUBLIC_KEY must be set explicitly")
		return ""
	}
	var paired map[string]struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(data, &paired); err != nil {
		return ""
	}
	if dev, ok := paired[deviceID]; ok {
		logger.Info().Str("deviceId", deviceID[:16]+"...").Msg("auto-detected public key from paired.json")
		return dev.PublicKey
	}
	return ""
}
