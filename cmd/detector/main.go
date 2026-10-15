// Command detector checks the MCP server and its upstream API for drift.
//
//	go run ./cmd/detector -update-baseline     # record the known-good state
//	go run ./cmd/detector                      # check once
//	go run ./cmd/detector -interval 1m         # poll
//
// Each new drift is written to <out>/<fingerprint>.json. A drift with the
// same fingerprint as an existing file is not reported again.
//
// Inside GitHub Actions (GITHUB_ACTIONS=true) it also writes the result to
// the job summary and emits an annotation per change.
//
// Exit code in single-run mode: 0 no drift, 2 needs a code fix, 3 needs a human.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SebbeJohansson/mcp-drift-investigation/internal/detector"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type options struct {
	cfg            detector.Config
	mcpCmd         string
	baselinePath   string
	outDir         string
	updateBaseline bool
}

func main() {
	var o options
	flag.StringVar(&o.cfg.APIURL, "api", envOr("SHOP_API_URL", "http://127.0.0.1:8100"), "upstream API base URL")
	flag.StringVar(&o.cfg.APIKey, "api-key", envOr("SHOP_API_KEY", "test-key-123"), "API key used by the MCP server")
	flag.StringVar(&o.cfg.ProbePath, "probe", "/v1/products", "authenticated GET used as health check")
	flag.DurationVar(&o.cfg.Timeout, "timeout", 3*time.Second, "HTTP timeout")
	flag.DurationVar(&o.cfg.SlowThreshold, "slow", time.Second, "latency above this is reported (non-breaking)")
	flag.StringVar(&o.mcpCmd, "mcp-cmd", "go run ./cmd/mockmcp", "command that starts the MCP server on stdio")
	flag.StringVar(&o.baselinePath, "baseline", "baseline/baseline.json", "baseline file")
	flag.StringVar(&o.outDir, "out", "events", "directory for drift events")
	flag.BoolVar(&o.updateBaseline, "update-baseline", false, "record the current state as the baseline and exit")
	interval := flag.Duration("interval", 0, "poll interval; 0 runs once")
	flag.Parse()

	ctx := context.Background()
	if o.updateBaseline {
		if err := updateBaseline(ctx, o); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *interval == 0 {
		ev, err := check(ctx, o)
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(map[string]int{detector.ActionNone: 0, detector.ActionSpawnAgent: 2, detector.ActionEscalate: 3}[ev.Action])
	}
	for {
		if _, err := check(ctx, o); err != nil {
			log.Print(err)
		}
		time.Sleep(*interval)
	}
}

// connect starts the MCP server as a subprocess, capturing its stderr.
func connect(ctx context.Context, o options) (*mcp.ClientSession, *bytes.Buffer, error) {
	args := strings.Fields(o.mcpCmd)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "SHOP_API_URL="+o.cfg.APIURL, "SHOP_API_KEY="+o.cfg.APIKey)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "drift-detector", Version: "0.1.0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("starting MCP server %q: %w (stderr: %s)", o.mcpCmd, err, stderr)
	}
	return cs, stderr, nil
}

func updateBaseline(ctx context.Context, o options) error {
	cs, _, err := connect(ctx, o)
	if err != nil {
		return err
	}
	defer cs.Close()
	base, err := detector.Capture(ctx, o.cfg, cs)
	if err != nil {
		return err
	}
	if err := base.Save(o.baselinePath); err != nil {
		return err
	}
	log.Printf("baseline written to %s (%d tools)", o.baselinePath, len(base.Tools))
	return nil
}

func check(ctx context.Context, o options) (detector.DriftEvent, error) {
	base, err := detector.LoadBaseline(o.baselinePath)
	if err != nil {
		return detector.DriftEvent{}, fmt.Errorf("loading baseline (run with -update-baseline first): %w", err)
	}
	cs, stderr, err := connect(ctx, o)
	if err != nil {
		return detector.DriftEvent{}, err
	}
	ev := detector.Detect(ctx, o.cfg, base, cs, "")
	cs.Close()
	ev.ServerLogs = tail(stderr.String(), 4000)

	log.Print(ev.Summary)
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		if err := reportToGitHub(ev); err != nil {
			log.Printf("writing GitHub summary: %v", err)
		}
	}
	if ev.Action == detector.ActionNone {
		return ev, nil
	}
	path := filepath.Join(o.outDir, ev.Fingerprint+".json")
	if _, err := os.Stat(path); err == nil {
		log.Printf("already reported: %s", path)
		return ev, nil
	}
	raw, _ := json.MarshalIndent(ev, "", "  ")
	if err := os.MkdirAll(o.outDir, 0o755); err != nil {
		return ev, err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return ev, err
	}
	log.Printf("drift event written: %s", path)
	return ev, nil
}

// reportToGitHub emits annotations and appends to the job summary.
func reportToGitHub(ev detector.DriftEvent) error {
	for _, a := range ev.Annotations() {
		fmt.Println(a)
	}
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(ev.Markdown())
	return err
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
