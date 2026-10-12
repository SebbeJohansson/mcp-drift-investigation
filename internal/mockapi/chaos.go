package mockapi

import (
	"fmt"
	"sort"
	"sync"
)

// Switch is a simulated upstream change. All switches are off by default.
type Switch struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

var Switches = []Switch{
	{"rename_field", "Product field `price` is renamed to `unit_price`"},
	{"remove_field", "Product field `stock` is removed"},
	{"required_param", "POST /orders requires a new `currency` field"},
	{"path_v2", "All endpoints move from /v1 to /v2; /v1 returns 404"},
	{"auth_bearer", "Auth moves from `X-API-Key` header to `Authorization: Bearer <key>`"},
	{"rotate_key", "The API key is rotated; the old key returns 401"},
	{"error_format", "Errors change from {detail} to {errors: [{code, message}]} and 404 becomes 410"},
	{"flaky_5xx", "Every request returns 503"},
	{"slow", "Every request sleeps 5s before responding"},
}

const (
	APIKey        = "test-key-123"
	RotatedAPIKey = "rotated-key-456"
)

// Chaos holds the set of active switches. Safe for concurrent use.
type Chaos struct {
	mu     sync.RWMutex
	active map[string]bool
}

func (c *Chaos) On(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active[name]
}

func (c *Chaos) Active() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := []string{}
	for n := range c.active {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Set replaces the active switches.
func (c *Chaos) Set(names ...string) error {
	known := map[string]bool{}
	for _, s := range Switches {
		known[s.Name] = true
	}
	next := map[string]bool{}
	for _, n := range names {
		if !known[n] {
			return fmt.Errorf("unknown chaos switch %q", n)
		}
		next[n] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = next
	return nil
}

func (c *Chaos) Version() string {
	if c.On("path_v2") {
		return "v2"
	}
	return "v1"
}

func (c *Chaos) ValidKey() string {
	if c.On("rotate_key") {
		return RotatedAPIKey
	}
	return APIKey
}
