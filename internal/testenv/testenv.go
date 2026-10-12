// Package testenv starts a mock API and an in-memory MCP session for tests.
package testenv

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revolutionrace/mcp-drift-investigation/internal/mockapi"
	"github.com/revolutionrace/mcp-drift-investigation/internal/shopmcp"
)

// Timeout is the HTTP timeout used by the MCP server and detector in tests.
const Timeout = 500 * time.Millisecond

type Env struct {
	API     *mockapi.API
	URL     string
	Session *mcp.ClientSession
}

func Start(t *testing.T) Env {
	t.Helper()
	ctx := context.Background()
	api := mockapi.New()
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)

	server := shopmcp.New(shopmcp.Config{BaseURL: srv.URL, APIKey: mockapi.APIKey, Timeout: Timeout})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return Env{API: api, URL: srv.URL, Session: cs}
}
