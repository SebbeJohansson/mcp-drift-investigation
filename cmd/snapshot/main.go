// Command snapshot writes each MCP tool's current output schema to
// internal/contracts/schemas/<tool>.json. Run it only against a known-good
// server: the output becomes the contract.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/SebbeJohansson/mcp-drift-investigation/internal/shopmcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const schemaDir = "internal/contracts/schemas"

func main() {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := shopmcp.New(shopmcp.Config{}).Connect(ctx, st, nil); err != nil {
		log.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "snapshot"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		log.Fatal(err)
	}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		raw, err := json.MarshalIndent(tool.OutputSchema, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(schemaDir, tool.Name+".json")
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", path)
	}
}
