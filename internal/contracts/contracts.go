// Package contracts calls each MCP tool against the live API and checks the result.
//
// A tool passes when it does not error and its structured output validates
// against the JSON schema stored in schemas/<tool>.json. The stored schemas
// are the contract; the agent must make the server meet them again, not edit
// them.
package contracts

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// SchemaDir is where cmd/snapshot writes schemas, relative to the repo root.
const SchemaDir = "internal/contracts/schemas"

type Result struct {
	Case   string `json:"case"`
	Tool   string `json:"tool"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
	Output any    `json:"output,omitempty"`
}

type Report struct {
	Results []Result `json:"results"`
}

func (r Report) Passed() bool { return len(r.Failures()) == 0 }

func (r Report) Failures() []Result {
	var out []Result
	for _, res := range r.Results {
		if !res.Passed {
			out = append(out, res)
		}
	}
	return out
}

func (r Report) FailingCases() []string {
	var out []string
	for _, f := range r.Failures() {
		out = append(out, f.Case)
	}
	return out
}

func loadSchema(tool string) (*jsonschema.Resolved, error) {
	raw, err := schemaFS.ReadFile("schemas/" + tool + ".json")
	if err != nil {
		return nil, err
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return s.Resolve(nil)
}

func text(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, " ")
}

type runner struct {
	cs     *mcp.ClientSession
	report Report
}

func (r *runner) fail(name, tool, format string, args ...any) {
	r.report.Results = append(r.report.Results, Result{Case: name, Tool: tool, Reason: fmt.Sprintf(format, args...)})
}

// ok calls tool and expects a successful result matching the stored schema.
func (r *runner) ok(ctx context.Context, name, tool string, args map[string]any) map[string]any {
	res, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		r.fail(name, tool, "call failed: %v", err)
		return nil
	}
	if res.IsError {
		r.fail(name, tool, "tool error: %s", text(res))
		return nil
	}
	// Round-trip through JSON so the validator sees plain maps and slices.
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		r.fail(name, tool, "structured output is not an object: %s", raw)
		return nil
	}
	schema, err := loadSchema(tool)
	if err != nil {
		r.fail(name, tool, "loading schema: %v", err)
		return nil
	}
	if err := schema.Validate(out); err != nil {
		r.fail(name, tool, "schema: %v", err)
		return nil
	}
	r.report.Results = append(r.report.Results, Result{Case: name, Tool: tool, Passed: true, Output: out})
	return out
}

// errors calls tool and expects an error result whose text matches pattern.
func (r *runner) errors(ctx context.Context, name, tool string, args map[string]any, pattern string) {
	res, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		r.fail(name, tool, "call failed: %v", err)
		return
	}
	got := text(res)
	if !res.IsError || !regexp.MustCompile(pattern).MatchString(got) {
		r.fail(name, tool, "expected error matching %q, got: %s", pattern, got)
		return
	}
	r.report.Results = append(r.report.Results, Result{Case: name, Tool: tool, Passed: true, Output: got})
}

func Run(ctx context.Context, cs *mcp.ClientSession) Report {
	r := &runner{cs: cs}
	r.ok(ctx, "list_products", "list_products", map[string]any{})
	r.ok(ctx, "get_product", "get_product", map[string]any{"product_id": "p1"})
	order := r.ok(ctx, "create_order", "create_order", map[string]any{"product_id": "p1", "quantity": 2})
	if order != nil {
		r.ok(ctx, "get_order", "get_order", map[string]any{"order_id": order["id"]})
	} else {
		r.fail("get_order", "get_order", "skipped: create_order failed")
	}
	r.errors(ctx, "get_product_not_found", "get_product", map[string]any{"product_id": "nope"},
		`404: product nope not found`)
	return r.report
}
