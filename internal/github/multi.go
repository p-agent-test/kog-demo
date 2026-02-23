package github

import (
	"context"
	"fmt"
	"strings"
	"sync"

	gogithub "github.com/google/go-github/v60/github"
	"github.com/rs/zerolog"

	"github.com/p-blackswan/platform-agent/pkg/tokenstore"
)

// OrgInstallation maps an org/owner name to its installation ID.
type OrgInstallation struct {
	Owner          string `json:"owner"`
	InstallationID int64  `json:"installation_id"`
}

// MultiClient manages multiple GitHub App installations (one per org).
// Thread-safe: clients are lazily created and cached.
type MultiClient struct {
	appID      int64
	keyPath    string
	store      tokenstore.Store
	logger     zerolog.Logger

	mu         sync.RWMutex
	orgs       map[string]int64    // owner → installationID
	clients    map[string]*Client  // owner → Client (lazy)
	fallback   string              // default owner when not specified
	singleOrg  bool                // true = any owner maps to fallback installation
}

// NewMultiClient creates a MultiClient from a list of org installations.
// The first org in the list becomes the default fallback.
func NewMultiClient(appID int64, keyPath string, orgs []OrgInstallation, store tokenstore.Store, logger zerolog.Logger) (*MultiClient, error) {
	if len(orgs) == 0 {
		return nil, fmt.Errorf("at least one org installation is required")
	}

	orgMap := make(map[string]int64, len(orgs))
	for _, o := range orgs {
		orgMap[strings.ToLower(o.Owner)] = o.InstallationID
	}

	return &MultiClient{
		appID:     appID,
		keyPath:   keyPath,
		store:     store,
		logger:    logger.With().Str("component", "github-multi").Logger(),
		orgs:      orgMap,
		clients:   make(map[string]*Client, len(orgs)),
		fallback:  strings.ToLower(orgs[0].Owner),
		singleOrg: len(orgs) == 1,
	}, nil
}

// ForOwner returns the Client for a specific org/owner.
// Creates the client lazily on first access.
func (m *MultiClient) ForOwner(owner string) (*Client, error) {
	key := strings.ToLower(owner)

	// Fast path: check cache
	m.mu.RLock()
	if c, ok := m.clients[key]; ok {
		m.mu.RUnlock()
		return c, nil
	}
	m.mu.RUnlock()

	// Slow path: create client
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if c, ok := m.clients[key]; ok {
		return c, nil
	}

	instID, ok := m.orgs[key]
	if !ok {
		// Single-org mode: any owner uses the fallback installation
		if m.singleOrg {
			instID = m.orgs[m.fallback]
			m.logger.Debug().Str("owner", owner).Str("fallback", m.fallback).Msg("single-org mode: using fallback installation")
		} else {
			return nil, fmt.Errorf("no GitHub installation configured for org %q (configured: %s)", owner, m.listOrgs())
		}
	}

	client, err := NewClient(m.appID, instID, m.keyPath, m.store, m.logger)
	if err != nil {
		return nil, fmt.Errorf("creating client for %s: %w", owner, err)
	}

	m.clients[key] = client
	m.logger.Info().Str("owner", owner).Int64("installation_id", instID).Msg("GitHub client created for org")
	return client, nil
}

// Default returns the fallback client (first configured org).
func (m *MultiClient) Default() (*Client, error) {
	return m.ForOwner(m.fallback)
}

// DefaultOwner returns the fallback org name.
func (m *MultiClient) DefaultOwner() string {
	return m.fallback
}

// GetInstallationClient returns a go-github client for the given owner.
// Convenience wrapper for ForOwner + GetInstallationClient.
func (m *MultiClient) GetInstallationClient(ctx context.Context, owner string) (*gogithub.Client, error) {
	c, err := m.ForOwner(owner)
	if err != nil {
		return nil, err
	}
	return c.GetInstallationClient(ctx)
}

// CreateScopedToken creates a scoped token via the correct org's installation.
func (m *MultiClient) CreateScopedToken(ctx context.Context, owner, repo string, permissions map[string]string) (*ScopedToken, error) {
	c, err := m.ForOwner(owner)
	if err != nil {
		return nil, err
	}
	return c.CreateScopedToken(ctx, repo, permissions)
}

// HasOwner checks if an org is configured.
func (m *MultiClient) HasOwner(owner string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.orgs[strings.ToLower(owner)]
	return ok
}

// Owners returns all configured org names.
func (m *MultiClient) Owners() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	owners := make([]string, 0, len(m.orgs))
	for o := range m.orgs {
		owners = append(owners, o)
	}
	return owners
}

func (m *MultiClient) listOrgs() string {
	owners := m.Owners()
	return strings.Join(owners, ", ")
}
