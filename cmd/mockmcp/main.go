// Command mockmcp runs the mock shop MCP server over stdio.
package main

import (
	"cmp"
	"context"
	"log"
	"os"
	"time"

	"github.com/SebbeJohansson/mcp-drift-investigation/internal/shopmcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := shopmcp.New(shopmcp.Config{
		BaseURL: cmp.Or(os.Getenv("SHOP_API_URL"), "http://127.0.0.1:8100"),
		APIKey:  cmp.Or(os.Getenv("SHOP_API_KEY"), "test-key-123"),
		Timeout: 3 * time.Second,
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
