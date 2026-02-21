package mgmt

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// Role defines the access level for an API key.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleReadOnly Role = "readonly"
)

// AuthConfig holds authentication configuration.
type AuthConfig struct {
	Mode   string          // "api-key", "mtls", "none"
	APIKey string          // from env MGMT_API_KEY
	Roles  map[string]Role // api-key → role mapping (future)
}

// NewAuthMiddleware returns a Fiber middleware that validates the Authorization header.
func NewAuthMiddleware(cfg AuthConfig, logger zerolog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Skip auth in "none" mode
		if cfg.Mode == "none" {
			c.Locals("role", RoleAdmin)
			return c.Next()
		}

		// Skip auth for probe endpoints
		path := c.Path()
		if path == "/healthz" || path == "/readyz" || path == "/metrics" {
			return c.Next()
		}

		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return problemResponse(c, fiber.StatusUnauthorized,
				"missing_auth", "Unauthorized",
				"Authorization header is required")
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			return problemResponse(c, fiber.StatusUnauthorized,
				"invalid_auth_scheme", "Unauthorized",
				"Authorization header must use Bearer scheme")
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Check against configured API key
		if cfg.APIKey != "" && token == cfg.APIKey {
			role := RoleAdmin
			if cfg.Roles != nil {
				if r, ok := cfg.Roles[token]; ok {
					role = r
				}
			}
			c.Locals("role", role)
			return c.Next()
		}

		// Check in roles map
		if cfg.Roles != nil {
			if role, ok := cfg.Roles[token]; ok {
				c.Locals("role", role)
				return c.Next()
			}
		}

		logger.Warn().
			Str("path", path).
			Str("method", c.Method()).
			Msg("unauthorized request: invalid API key")

		return problemResponse(c, fiber.StatusUnauthorized,
			"invalid_api_key", "Unauthorized",
			"Invalid API key")
	}
}

// requireRole returns a middleware that enforces a minimum role level.
func requireRole(minRole Role) fiber.Handler {
	roleLevel := map[Role]int{
		RoleReadOnly: 1,
		RoleOperator: 2,
		RoleAdmin:    3,
	}

	return func(c *fiber.Ctx) error {
		role, _ := c.Locals("role").(Role)
		if roleLevel[role] < roleLevel[minRole] {
			return problemResponse(c, fiber.StatusForbidden,
				"insufficient_role", "Forbidden",
				"Insufficient permissions for this operation")
		}
		return c.Next()
	}
}

// problemResponse returns an RFC 7807 Problem Detail error response.
func problemResponse(c *fiber.Ctx, status int, errType, title, detail string) error {
	return c.Status(status).JSON(ProblemDetail{
		Type:     errType,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: c.Path(),
	})
}
