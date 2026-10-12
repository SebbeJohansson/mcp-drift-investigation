package contracts_test

import (
	"context"
	"testing"

	"github.com/revolutionrace/mcp-drift-investigation/internal/contracts"
	"github.com/revolutionrace/mcp-drift-investigation/internal/mockapi"
	"github.com/revolutionrace/mcp-drift-investigation/internal/testenv"
)

func TestContractsPassWithoutChaos(t *testing.T) {
	env := testenv.Start(t)
	report := contracts.Run(context.Background(), env.Session)
	if !report.Passed() {
		t.Fatalf("contracts failed: %+v", report.Failures())
	}
}

func TestEachChaosSwitchBreaksAContract(t *testing.T) {
	for _, sw := range mockapi.Switches {
		t.Run(sw.Name, func(t *testing.T) {
			t.Parallel()
			env := testenv.Start(t)
			if err := env.API.Chaos.Set(sw.Name); err != nil {
				t.Fatal(err)
			}
			report := contracts.Run(context.Background(), env.Session)
			if report.Passed() {
				t.Fatalf("%s did not break any contract", sw.Name)
			}
			t.Logf("failing: %v", report.FailingCases())
		})
	}
}
