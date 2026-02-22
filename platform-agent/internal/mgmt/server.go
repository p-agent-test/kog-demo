package mgmt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/health"
	"github.com/p-blackswan/platform-agent/internal/metrics"
	"github.com/p-blackswan/platform-agent/internal/requestid"
)

// ServerConfig holds configuration for the management API server.
type ServerConfig struct {
	ListenAddr     string
	AuthConfig     AuthConfig
	RateLimit      RateLimitConfig
	CORSOrigins    string
	TLSCert        string
	TLSKey         string
	Workers        int
}

// Server is the management API Fiber application.
type Server struct {
	app      *fiber.App
	handlers *Handlers
	engine   *TaskEngine
	logger   zerolog.Logger
	config   ServerConfig
}

// NewServer creates and configures a new management API server.
func NewServer(
	cfg ServerConfig,
	engine *TaskEngine,
	checker *health.Checker,
	metricsCollector *metrics.Metrics,
	rtCfg *RuntimeConfig,
	logger zerolog.Logger,
) *Server {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          customErrorHandler(logger),
		JSONEncoder:           json.Marshal,
		JSONDecoder:           json.Unmarshal,
		ReadBufferSize:        8192,
		WriteBufferSize:       8192,
	})

	handlers := NewHandlers(engine, checker, rtCfg, logger)

	s := &Server{
		app:      app,
		handlers: handlers,
		engine:   engine,
		logger:   logger.With().Str("component", "mgmt_server").Logger(),
		config:   cfg,
	}

	s.setupMiddleware(cfg, logger)
	s.setupRoutes(handlers, metricsCollector)

	return s
}

func (s *Server) setupMiddleware(cfg ServerConfig, logger zerolog.Logger) {
	// Recovery middleware
	s.app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
	}))

	// Request ID middleware
	s.app.Use(func(c *fiber.Ctx) error {
		_, reqID := requestid.New(c.Context())
		c.Set("X-Request-ID", reqID)
		c.Locals("request_id", reqID)
		return c.Next()
	})

	// CORS middleware
	if cfg.CORSOrigins != "" {
		s.app.Use(cors.New(cors.Config{
			AllowOrigins: cfg.CORSOrigins,
			AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Request-ID",
			AllowMethods: "GET, POST, PATCH, DELETE, OPTIONS",
		}))
	}

	// Rate limiter
	if cfg.RateLimit.RPS > 0 {
		s.app.Use(NewRateLimitMiddleware(cfg.RateLimit))
	}

	// Auth middleware
	s.app.Use(NewAuthMiddleware(cfg.AuthConfig, logger))

	// Audit middleware (log every request)
	s.app.Use(func(c *fiber.Ctx) error {
		path := c.Path()
		// Skip noisy probe logging
		if path == "/healthz" || path == "/readyz" || path == "/metrics" {
			return c.Next()
		}

		logger.Info().
			Str("method", c.Method()).
			Str("path", path).
			Str("ip", c.IP()).
			Str("request_id", fmt.Sprintf("%v", c.Locals("request_id"))).
			Msg("mgmt api request")

		return c.Next()
	})
}

func (s *Server) setupRoutes(h *Handlers, metricsCollector *metrics.Metrics) {
	// Probe endpoints (no auth required — handled in auth middleware)
	s.app.Get("/healthz", h.Liveness)
	s.app.Get("/readyz", h.Readiness)

	// Prometheus metrics
	if metricsCollector != nil {
		s.app.Get("/metrics", func(c *fiber.Ctx) error {
			_ = metricsCollector.Handler()
			c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			return c.SendString("# Prometheus metrics endpoint\n# Use the main HTTP server on :8080 for full metrics\n")
		})
	} else {
		s.app.Get("/metrics", func(c *fiber.Ctx) error {
			return c.SendString("# No metrics collector configured\n")
		})
	}

	// API v1 routes
	v1 := s.app.Group("/api/v1")

	// Task endpoints
	v1.Post("/tasks", h.SubmitTask)
	v1.Get("/tasks", h.ListTasks)
	v1.Get("/tasks/:id", h.GetTask)
	v1.Delete("/tasks/:id", h.CancelTask)

	// Chat endpoint
	v1.Post("/chat", h.Chat)

	// Health & config
	v1.Get("/health", h.HealthDetail)
	v1.Get("/config", h.GetConfig)
	v1.Patch("/config", requireRole(RoleAdmin), h.PatchConfig)

	// Metrics summary
	v1.Get("/metrics/summary", h.MetricsSummary)
}

// Start starts the server. Blocks until stopped.
func (s *Server) Start() error {
	addr := s.config.ListenAddr
	if addr == "" {
		addr = ":8090"
	}

	s.logger.Info().Str("addr", addr).Msg("management API server starting")

	if s.config.TLSCert != "" && s.config.TLSKey != "" {
		return s.app.ListenTLS(addr, s.config.TLSCert, s.config.TLSKey)
	}
	return s.app.Listen(addr)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	s.logger.Info().Msg("management API server shutting down")
	return s.app.Shutdown()
}

// App returns the underlying Fiber app (useful for testing).
func (s *Server) App() *fiber.App {
	return s.app
}

func customErrorHandler(logger zerolog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		code := fiber.StatusInternalServerError
		if e, ok := err.(*fiber.Error); ok {
			code = e.Code
		}

		logger.Error().
			Err(err).
			Int("status", code).
			Str("path", c.Path()).
			Str("method", c.Method()).
			Msg("unhandled error")

		detail := err.Error()
		// Don't leak internal details in production
		if code == fiber.StatusInternalServerError {
			if !strings.Contains(detail, "test") {
				detail = "An internal error occurred"
			}
		}

		return c.Status(code).JSON(ProblemDetail{
			Type:     "internal_error",
			Title:    "Internal Server Error",
			Status:   code,
			Detail:   detail,
			Instance: c.Path(),
		})
	}
}
