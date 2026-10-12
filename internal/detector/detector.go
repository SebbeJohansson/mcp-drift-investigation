// Package detector finds drift between an MCP server and the API it wraps.
//
// It compares the API's OpenAPI spec and the server's tools/list against a
// stored baseline, runs the contract tests, probes API health, and turns the
// result into a DriftEvent that says whether to spawn a fixing agent.
package detector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revolutionrace/mcp-drift-investigation/internal/contracts"
)

const (
	ActionNone       = "none"
	ActionSpawnAgent = "spawn_agent"
	ActionEscalate   = "escalate"
)

type Config struct {
	APIURL string
	APIKey string
	// ProbePath is an authenticated GET used for the health check. It uses the
	// same path and auth as the MCP server, so it fails the same way.
	ProbePath     string
	Timeout       time.Duration
	SlowThreshold time.Duration
}

type ToolInfo struct {
	Name         string `json:"name"`
	InputSchema  any    `json:"inputSchema"`
	OutputSchema any    `json:"outputSchema,omitempty"`
}

// Baseline is the known-good state that later runs are compared against.
type Baseline struct {
	Spec  obj        `json:"openapi"`
	Tools []ToolInfo `json:"tools"`
}

type Health struct {
	Status    int    `json:"status,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type DriftEvent struct {
	Fingerprint string             `json:"fingerprint"`
	DetectedAt  time.Time          `json:"detected_at"`
	APIURL      string             `json:"api_url"`
	Action      string             `json:"action"`
	Reason      string             `json:"reason"`
	Summary     string             `json:"summary"`
	Changes     []Change           `json:"changes"`
	FailedTools []contracts.Result `json:"failed_contracts,omitempty"`
	Health      Health             `json:"health"`
	ServerLogs  string             `json:"server_logs,omitempty"`
}

func (e DriftEvent) Breaking() []Change {
	var out []Change
	for _, c := range e.Changes {
		if c.Breaking {
			out = append(out, c)
		}
	}
	return out
}

// Capture records the current spec and tool list as a baseline.
func Capture(ctx context.Context, cfg Config, cs *mcp.ClientSession) (Baseline, error) {
	spec, err := fetchSpec(ctx, cfg)
	if err != nil {
		return Baseline{}, err
	}
	tools, err := listTools(ctx, cs)
	if err != nil {
		return Baseline{}, err
	}
	return Baseline{Spec: spec, Tools: tools}, nil
}

func LoadBaseline(path string) (Baseline, error) {
	var b Baseline
	raw, err := os.ReadFile(path)
	if err != nil {
		return b, err
	}
	return b, json.Unmarshal(raw, &b)
}

func (b Baseline) Save(path string) error {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// Detect compares the current state with the baseline. serverLogs is the
// MCP server's stderr, passed through so the agent can see it.
func Detect(ctx context.Context, cfg Config, base Baseline, cs *mcp.ClientSession, serverLogs string) DriftEvent {
	ev := DriftEvent{DetectedAt: time.Now().UTC(), APIURL: cfg.APIURL, ServerLogs: serverLogs}

	health, healthChanges := probe(ctx, cfg)
	ev.Health = health
	ev.Changes = append(ev.Changes, healthChanges...)

	if spec, err := fetchSpec(ctx, cfg); err != nil {
		ev.Changes = append(ev.Changes, Change{"openapi", "spec_unavailable", "/openapi.json", err.Error(), false})
	} else {
		ev.Changes = append(ev.Changes, DiffSpecs(base.Spec, spec)...)
	}

	if tools, err := listTools(ctx, cs); err != nil {
		ev.Changes = append(ev.Changes, Change{"tools", "tools_list_failed", "tools/list", err.Error(), true})
	} else {
		ev.Changes = append(ev.Changes, diffTools(base.Tools, tools)...)
	}

	report := contracts.Run(ctx, cs)
	ev.FailedTools = report.Failures()
	for _, f := range ev.FailedTools {
		ev.Changes = append(ev.Changes, Change{"contracts", "contract_failed", f.Case, f.Reason, true})
	}

	ev.Action, ev.Reason = classify(ev.Changes)
	ev.Fingerprint = fingerprint(ev.Breaking())
	ev.Summary = summarize(ev)
	return ev
}

// classify decides what to do. Code drift goes to an agent; outages and
// rejected credentials go to a human, since no code change fixes them.
func classify(changes []Change) (action, reason string) {
	var breaking, securityChanged, authFailed, outage bool
	for _, c := range changes {
		breaking = breaking || c.Breaking
		switch c.Kind {
		case "security_changed":
			securityChanged = true
		case "auth_failed":
			authFailed = true
		case "unreachable", "server_error":
			outage = true
		}
	}
	switch {
	case !breaking:
		return ActionNone, "no breaking changes"
	case outage:
		return ActionEscalate, "upstream API is down or erroring; not a code change"
	case authFailed && !securityChanged:
		return ActionEscalate, "credentials rejected but auth scheme unchanged; likely a rotated secret"
	default:
		return ActionSpawnAgent, "breaking upstream change the MCP server must adapt to"
	}
}

func probe(ctx context.Context, cfg Config) (Health, []Change) {
	loc := "GET " + cfg.ProbePath
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.APIURL+cfg.ProbePath, nil)
	if err != nil {
		return Health{Error: err.Error()}, []Change{{"health", "unreachable", loc, err.Error(), true}}
	}
	req.Header.Set("X-API-Key", cfg.APIKey)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	h := Health{LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		h.Error = err.Error()
		detail := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			detail = fmt.Sprintf("timed out after %s", cfg.Timeout)
		}
		return h, []Change{{"health", "unreachable", loc, detail, true}}
	}
	resp.Body.Close()
	h.Status = resp.StatusCode

	var out []Change
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		out = append(out, Change{"health", "auth_failed", loc, resp.Status, true})
	case resp.StatusCode >= 500:
		out = append(out, Change{"health", "server_error", loc, resp.Status, true})
	case resp.StatusCode >= 400:
		out = append(out, Change{"health", "probe_failed", loc, resp.Status, true})
	}
	if cfg.SlowThreshold > 0 && time.Since(start) > cfg.SlowThreshold {
		out = append(out, Change{"health", "slow", loc, fmt.Sprintf("%dms", h.LatencyMS), false})
	}
	return h, out
}

func fetchSpec(ctx context.Context, cfg Config) (obj, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.APIURL+"/openapi.json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /openapi.json: %s", resp.Status)
	}
	var spec obj
	return spec, json.NewDecoder(resp.Body).Decode(&spec)
}

func listTools(ctx context.Context, cs *mcp.ClientSession) ([]ToolInfo, error) {
	var out []ToolInfo
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		info := ToolInfo{Name: t.Name, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema}
		// Normalize to plain JSON values so it compares equal to a loaded baseline.
		raw, _ := json.Marshal(info)
		var norm ToolInfo
		_ = json.Unmarshal(raw, &norm)
		out = append(out, norm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func diffTools(base, cur []ToolInfo) []Change {
	var out []Change
	curByName := map[string]ToolInfo{}
	for _, t := range cur {
		curByName[t.Name] = t
	}
	for _, b := range base {
		c, ok := curByName[b.Name]
		delete(curByName, b.Name)
		switch {
		case !ok:
			out = append(out, Change{"tools", "tool_removed", b.Name, "tool removed", true})
		case !reflect.DeepEqual(b.InputSchema, c.InputSchema):
			out = append(out, Change{"tools", "input_schema_changed", b.Name, "input schema changed", true})
		case !reflect.DeepEqual(b.OutputSchema, c.OutputSchema):
			out = append(out, Change{"tools", "output_schema_changed", b.Name, "output schema changed", true})
		}
	}
	for _, name := range sortedKeys(curByName) {
		out = append(out, Change{"tools", "tool_added", name, "tool added", false})
	}
	return out
}

// fingerprint identifies a drift so repeated polls don't re-trigger an agent.
func fingerprint(breaking []Change) string {
	if len(breaking) == 0 {
		return ""
	}
	var parts []string
	for _, c := range breaking {
		parts = append(parts, c.Source+"|"+c.Kind+"|"+c.Location)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])[:12]
}

func summarize(ev DriftEvent) string {
	bySource := map[string]int{}
	for _, c := range ev.Breaking() {
		bySource[c.Source]++
	}
	var parts []string
	for _, s := range sortedKeys(bySource) {
		parts = append(parts, fmt.Sprintf("%s=%d", s, bySource[s]))
	}
	return fmt.Sprintf("%d breaking (%s) -> %s", len(ev.Breaking()), strings.Join(parts, ", "), ev.Action)
}
