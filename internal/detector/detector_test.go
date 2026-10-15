package detector_test

import (
	"context"
	"testing"

	"github.com/SebbeJohansson/mcp-drift-investigation/internal/detector"
	"github.com/SebbeJohansson/mcp-drift-investigation/internal/mockapi"
	"github.com/SebbeJohansson/mcp-drift-investigation/internal/testenv"
)

func setup(t *testing.T) (testenv.Env, detector.Config, detector.Baseline) {
	t.Helper()
	env := testenv.Start(t)
	cfg := detector.Config{
		APIURL:    env.URL,
		APIKey:    mockapi.APIKey,
		ProbePath: "/v1/products",
		Timeout:   testenv.Timeout,
	}
	base, err := detector.Capture(context.Background(), cfg, env.Session)
	if err != nil {
		t.Fatal(err)
	}
	return env, cfg, base
}

func TestNoDriftWithoutChaos(t *testing.T) {
	env, cfg, base := setup(t)
	ev := detector.Detect(context.Background(), cfg, base, env.Session, "")
	if ev.Action != detector.ActionNone || len(ev.Changes) != 0 {
		t.Fatalf("expected no drift, got %s: %+v", ev.Summary, ev.Changes)
	}
}

func TestEachSwitchIsDetectedAndClassified(t *testing.T) {
	cases := map[string]struct {
		action   string
		mustFind string // a breaking change kind that must be present
	}{
		"rename_field":   {detector.ActionSpawnAgent, "possible_rename"},
		"remove_field":   {detector.ActionSpawnAgent, "property_removed"},
		"required_param": {detector.ActionSpawnAgent, "required_added"},
		"path_v2":        {detector.ActionSpawnAgent, "operation_removed"},
		"auth_bearer":    {detector.ActionSpawnAgent, "security_changed"},
		"rotate_key":     {detector.ActionEscalate, "auth_failed"},
		"error_format":   {detector.ActionSpawnAgent, "response_code_removed"},
		"flaky_5xx":      {detector.ActionEscalate, "server_error"},
		"slow":           {detector.ActionEscalate, "unreachable"},
	}
	if len(cases) != len(mockapi.Switches) {
		t.Fatalf("test covers %d switches, mockapi has %d", len(cases), len(mockapi.Switches))
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, cfg, base := setup(t)
			if err := env.API.Chaos.Set(name); err != nil {
				t.Fatal(err)
			}
			ev := detector.Detect(context.Background(), cfg, base, env.Session, "")
			if ev.Action != want.action {
				t.Errorf("action = %s (%s), want %s", ev.Action, ev.Reason, want.action)
			}
			found := false
			for _, c := range ev.Breaking() {
				found = found || c.Kind == want.mustFind
			}
			if !found {
				t.Errorf("no breaking %q change in %+v", want.mustFind, ev.Changes)
			}
			if ev.Fingerprint == "" {
				t.Error("empty fingerprint")
			}
			t.Log(ev.Summary)
		})
	}
}

func TestFingerprintIsStableAcrossRuns(t *testing.T) {
	env, cfg, base := setup(t)
	env.API.Chaos.Set("rename_field")
	a := detector.Detect(context.Background(), cfg, base, env.Session, "")
	b := detector.Detect(context.Background(), cfg, base, env.Session, "")
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprints differ: %s vs %s", a.Fingerprint, b.Fingerprint)
	}
}
