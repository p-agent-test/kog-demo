// Command kog is the Kog Runtime binary — an autonomous, event-driven AI agent.
//
// Usage:
//
//	KOG_ANTHROPIC_API_KEY=sk-... KOG_TELEGRAM_TOKEN=12345:ABC... kog
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/p-blackswan/platform-agent/internal/escalation"
	"github.com/p-blackswan/platform-agent/internal/kogagent"
	"github.com/p-blackswan/platform-agent/internal/event"
	"github.com/p-blackswan/platform-agent/internal/llm"
	"github.com/p-blackswan/platform-agent/internal/memory"
	"github.com/p-blackswan/platform-agent/internal/runtime"
	"github.com/p-blackswan/platform-agent/internal/tool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// ---- Config from env ----
	anthropicKey := mustEnv("KOG_ANTHROPIC_API_KEY")
	telegramToken := os.Getenv("KOG_TELEGRAM_TOKEN")
	escalateChatIDStr := os.Getenv("KOG_ESCALATE_CHAT_ID")
	dbPath := envOr("KOG_DB_PATH", "kog.db")
	modelID := envOr("KOG_MODEL", "claude-sonnet-4-5")

	// ---- Memory ----
	store, err := memory.NewSQLiteStore(dbPath, logger)
	if err != nil {
		logger.Error("failed to open memory store", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	// ---- LLM Provider ----
	provider := llm.NewAnthropicProvider(
		anthropicKey,
		llm.WithModel(modelID),
		llm.WithLogger(logger),
	)

	// ---- Tools ----
	registry := tool.NewRegistry()
	registry.Register(tool.NewExecTool("", 0, logger))
	registry.Register(tool.NewHTTPTool(logger))

	// ---- System Prompt ----
	systemPrompt := `You are Kog — an autonomous AI agent built on the Kog Runtime.
You receive events from various sources (Telegram messages, cron ticks, webhooks) and act on them.
You have access to tools: exec (run shell commands) and http_request (make HTTP calls).
Be concise, decisive, and always complete your tasks. When uncertain, escalate to the human.`

	// ---- Agent ----
	a, err := kogagent.New(kogagent.Spec{
		ID:           "kog-primary",
		SystemPrompt: systemPrompt,
		Provider:     provider,
		Registry:     registry,
		Memory:       store,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("failed to create agent", "err", err)
		os.Exit(1)
	}

	// ---- Runtime ----
	cfg := runtime.DefaultConfig()
	rt := runtime.New(cfg, logger)
	rt.AddAgent(a)

	// ---- Event Sources ----
	if telegramToken != "" {
		tgSrc := event.NewTelegramSource(telegramToken,
			event.TelegramWithLogger(logger),
		)
		rt.AddSource(tgSrc)
		logger.Info("telegram source registered")
	} else {
		logger.Warn("KOG_TELEGRAM_TOKEN not set — telegram source disabled")
	}

	// Add a heartbeat cron job (every 5 minutes).
	cronSrc := event.NewCronSource([]event.CronJob{
		{Name: "heartbeat", Interval: 5 * 60 * 1e9, Spec: "*/5 * * * *"}, // 5min
	}, logger)
	rt.AddSource(cronSrc)

	// ---- Escalation ----
	var notifier escalation.Notifier = escalation.NewLogNotifier(logger)
	if telegramToken != "" && escalateChatIDStr != "" {
		chatID, err := strconv.ParseInt(escalateChatIDStr, 10, 64)
		if err == nil {
			tgNotifier := escalation.NewTelegramNotifier(telegramToken, chatID, logger)
			notifier = escalation.NewMultiNotifier(escalation.NewLogNotifier(logger), tgNotifier)
			logger.Info("escalation: telegram notifier active", "chat_id", chatID)
		}
	}
	_ = notifier // wired into runtime in future PR

	// ---- Run ----
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("kog runtime starting", "model", modelID, "db", dbPath)
	if err := rt.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("runtime error", "err", err)
		os.Exit(1)
	}

	logger.Info("kog runtime stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required env var not set", "key", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
