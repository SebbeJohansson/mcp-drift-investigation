package detector

import (
	"fmt"
	"strings"
)

// Status is a one-line human verdict for the event.
func (e DriftEvent) Status() string {
	switch e.Action {
	case ActionSpawnAgent:
		return "🔴 Breaking drift: the MCP server needs a code fix"
	case ActionEscalate:
		return "🟠 Upstream problem: needs a human"
	default:
		return "✅ No drift"
	}
}

// Markdown renders the event for a GitHub Actions job summary.
func (e DriftEvent) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## MCP drift check: %s\n\n", e.Status())
	fmt.Fprintf(&b, "| | |\n| -- | -- |\n")
	fmt.Fprintf(&b, "| Reason | %s |\n", cell(e.Reason))
	fmt.Fprintf(&b, "| API | `%s` |\n", e.APIURL)
	health := fmt.Sprintf("HTTP %d in %dms", e.Health.Status, e.Health.LatencyMS)
	if e.Health.Error != "" {
		health = "error: " + e.Health.Error
	}
	fmt.Fprintf(&b, "| Health probe | %s |\n", cell(health))
	if e.Fingerprint != "" {
		fmt.Fprintf(&b, "| Fingerprint | `%s` |\n", e.Fingerprint)
	}
	fmt.Fprintf(&b, "| Checked at | %s |\n\n", e.DetectedAt.Format("2006-01-02 15:04:05 UTC"))

	if len(e.Changes) == 0 {
		b.WriteString("Spec, tools/list, contracts and health all match the baseline.\n")
		return b.String()
	}
	b.WriteString("### Changes\n\n| | Source | Kind | Location | Detail |\n| -- | -- | -- | -- | -- |\n")
	for _, c := range e.Changes {
		mark := "⚪"
		if c.Breaking {
			mark = "🔴"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | `%s` | %s |\n", mark, c.Source, c.Kind, cell(c.Location), cell(c.Detail))
	}
	b.WriteString("\n🔴 breaking · ⚪ non-breaking\n")
	return b.String()
}

// Annotations renders GitHub workflow commands: an error per breaking
// change and a warning per non-breaking one.
func (e DriftEvent) Annotations() []string {
	var out []string
	for _, c := range e.Changes {
		level := "warning"
		if c.Breaking {
			level = "error"
		}
		title := fmt.Sprintf("%s %s", c.Source, c.Kind)
		out = append(out, fmt.Sprintf("::%s title=%s::%s", level, escapeProperty(title), escapeData(c.Location+": "+c.Detail)))
	}
	return out
}

func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}
