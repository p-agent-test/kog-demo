package mgmt

import (
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/internal/project"
)

// ProjectHandlers holds dependencies for project API handlers.
type ProjectHandlers struct {
	store   *project.Store
	manager *project.Manager
	logger  zerolog.Logger
}

// NewProjectHandlers creates new project API handlers.
func NewProjectHandlers(store *project.Store, manager *project.Manager, logger zerolog.Logger) *ProjectHandlers {
	return &ProjectHandlers{
		store:   store,
		manager: manager,
		logger:  logger.With().Str("component", "project_handlers").Logger(),
	}
}

// RegisterRoutes registers project API routes on the given fiber group.
func (h *ProjectHandlers) RegisterRoutes(v1 fiber.Router) {
	pg := v1.Group("/projects")
	pg.Post("/", h.CreateProject)
	pg.Get("/", h.ListProjects)
	pg.Get("/:slug", h.GetProject)
	pg.Patch("/:slug", h.UpdateProject)
	pg.Post("/:slug/memory", h.AddMemory)
	pg.Get("/:slug/memory", h.ListMemory)
	pg.Get("/:slug/events", h.ListEvents)
	pg.Post("/:slug/archive", h.Archive)
	pg.Post("/:slug/resume", h.Resume)
	pg.Delete("/:slug", h.DeleteProject)
	pg.Post("/:slug/message", h.SendMessage)
}

func (h *ProjectHandlers) CreateProject(c *fiber.Ctx) error {
	var req project.CreateProjectInput
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.Status(400).JSON(fiber.Map{"error": "name is required"})
	}
	if req.OwnerID == "" {
		req.OwnerID = "api"
	}

	p, err := h.store.CreateProject(req)
	if err != nil {
		if contains(err.Error(), "already exists") {
			return c.Status(409).JSON(fiber.Map{"error": err.Error()})
		}
		if contains(err.Error(), "reserved word") {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(201).JSON(p)
}

func (h *ProjectHandlers) ListProjects(c *fiber.Ctx) error {
	status := c.Query("status", "")
	ownerID := c.Query("owner_id", "")

	projects, err := h.store.ListProjects(status, ownerID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if projects == nil {
		projects = []*project.Project{}
	}

	return c.JSON(fiber.Map{"projects": projects, "total": len(projects)})
}

func (h *ProjectHandlers) GetProject(c *fiber.Ctx) error {
	slug := c.Params("slug")
	p, err := h.store.GetProject(slug)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "project not found"})
	}

	// Include recent memory and events
	memory, _ := h.store.ListMemory(p.ID, "")
	events, _ := h.store.ListEvents(p.ID, 20)
	stats, _ := h.store.GetProjectStats(p.ID)

	return c.JSON(fiber.Map{
		"project":       p,
		"recent_memory": memory,
		"recent_events": events,
		"stats":         stats,
	})
}

func (h *ProjectHandlers) UpdateProject(c *fiber.Ctx) error {
	slug := c.Params("slug")
	var req project.UpdateProjectInput
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}

	p, err := h.store.UpdateProject(slug, req)
	if err != nil {
		if contains(err.Error(), "not found") {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(p)
}

func (h *ProjectHandlers) AddMemory(c *fiber.Ctx) error {
	slug := c.Params("slug")
	p, err := h.store.GetProject(slug)
	if err != nil || p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "project not found"})
	}

	var req struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Type == "" || req.Content == "" {
		return c.Status(400).JSON(fiber.Map{"error": "type and content are required"})
	}

	mem := &project.ProjectMemory{
		ProjectID: p.ID,
		Type:      req.Type,
		Content:   req.Content,
	}
	if err := h.store.AddMemory(mem); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(201).JSON(mem)
}

func (h *ProjectHandlers) ListMemory(c *fiber.Ctx) error {
	slug := c.Params("slug")
	p, err := h.store.GetProject(slug)
	if err != nil || p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "project not found"})
	}

	memType := c.Query("type", "")
	mems, err := h.store.ListMemory(p.ID, memType)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if mems == nil {
		mems = []*project.ProjectMemory{}
	}

	return c.JSON(fiber.Map{"memory": mems})
}

func (h *ProjectHandlers) ListEvents(c *fiber.Ctx) error {
	slug := c.Params("slug")
	p, err := h.store.GetProject(slug)
	if err != nil || p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "project not found"})
	}

	events, err := h.store.ListEvents(p.ID, 50)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if events == nil {
		events = []*project.ProjectEvent{}
	}

	return c.JSON(fiber.Map{"events": events})
}

func (h *ProjectHandlers) Archive(c *fiber.Ctx) error {
	slug := c.Params("slug")
	actorID := c.Query("actor_id", "api")

	if err := h.store.ArchiveProject(slug, actorID); err != nil {
		if contains(err.Error(), "not found") {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"status": "archived"})
}

func (h *ProjectHandlers) Resume(c *fiber.Ctx) error {
	slug := c.Params("slug")
	actorID := c.Query("actor_id", "api")

	p, err := h.manager.ResumeProject(slug, actorID)
	if err != nil {
		if contains(err.Error(), "not found") || contains(err.Error(), "not archived") {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(p)
}

func (h *ProjectHandlers) DeleteProject(c *fiber.Ctx) error {
	slug := c.Params("slug")
	if err := h.store.DeleteProject(slug); err != nil {
		if contains(err.Error(), "not found") {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.SendStatus(204)
}

func (h *ProjectHandlers) SendMessage(c *fiber.Ctx) error {
	slug := c.Params("slug")
	p, err := h.store.GetProject(slug)
	if err != nil || p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "project not found"})
	}

	var req struct {
		Message         string `json:"message"`
		CallerID        string `json:"caller_id"`
		ResponseChannel string `json:"response_channel"`
		ResponseThread  string `json:"response_thread"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Message == "" {
		return c.Status(400).JSON(fiber.Map{"error": "message is required"})
	}

	// Record the message event
	_ = h.store.AddEvent(&project.ProjectEvent{
		ProjectID: p.ID,
		EventType: "message",
		ActorID:   req.CallerID,
		Summary:   req.Message,
	})

	return c.Status(202).JSON(fiber.Map{
		"session": p.ActiveSession,
		"status":  "accepted",
	})
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
