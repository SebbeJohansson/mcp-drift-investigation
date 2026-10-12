package contracts_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revolutionrace/mcp-drift-investigation/internal/contracts"
	"github.com/revolutionrace/mcp-drift-investigation/internal/mockapi"
	"github.com/revolutionrace/mcp-drift-investigation/internal/shopmcp"
)

// connect starts a fresh mock API and an in-memory MCP session against it.
func connect(t *testing.T) (*mockapi.API, *mcp.ClientSession) {
	t.Helper()
	ctx := context.Background()
	api := mockapi.New()
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)

	server := shopmcp.New(shopmcp.Config{BaseURL: srv.URL, APIKey: mockapi.APIKey, Timeout: 500 * time.Millisecond})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "contracts-test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return api, cs
}

func TestContractsPassWithoutChaos(t *testing.T) {
	_, cs := connect(t)
	report := contracts.Run(context.Background(), cs)
	if !report.Passed() {
		t.Fatalf("contracts failed: %+v", report.Failures())
	}
}

func TestEachChaosSwitchBreaksAContract(t *testing.T) {
	for _, sw := range mockapi.Switches {
		t.Run(sw.Name, func(t *testing.T) {
			t.Parallel()
			api, cs := connect(t)
			if err := api.Chaos.Set(sw.Name); err != nil {
				t.Fatal(err)
			}
			report := contracts.Run(context.Background(), cs)
			if report.Passed() {
				t.Fatalf("%s did not break any contract", sw.Name)
			}
			t.Logf("failing: %v", report.FailingCases())
		})
	}
}
