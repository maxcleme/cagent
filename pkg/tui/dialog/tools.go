package dialog

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/lifecycle"
	"github.com/docker/docker-agent/pkg/tui/components/toolcommon"
	"github.com/docker/docker-agent/pkg/tui/styles"
)

// toolsDialog is the unified "/tools" view: a Toolsets section showing
// each toolset's lifecycle state, restart count and last error, followed
// by the Tools section grouping individual tools by their reported
// Category.
type toolsDialog struct {
	readOnlyScrollDialog

	toolsets []tools.ToolsetStatus
	tools    []tools.Tool
}

// NewToolsDialog creates the unified /tools dialog, showing the lifecycle
// status of each toolset (top section) followed by every tool exposed to
// the agent (bottom section, grouped by category).
//
// The two halves are intentionally rendered together: a tool's Category
// is generally a functional bucket ("filesystem", "shell", "lsp"), not
// the toolset name, so a separate /toolsets dialog used to be the only
// way for users to see lifecycle state. Combining them here means a
// single command surfaces both "what can the agent do" and "is anything
// degraded".
func NewToolsDialog(toolsets []tools.ToolsetStatus, toolList []tools.Tool) Dialog {
	// Sort tools by category then name.
	sortedTools := make([]tools.Tool, len(toolList))
	copy(sortedTools, toolList)
	slices.SortFunc(sortedTools, func(a, b tools.Tool) int {
		if c := strings.Compare(strings.ToLower(a.Category), strings.ToLower(b.Category)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.DisplayName()), strings.ToLower(b.DisplayName()))
	})

	d := &toolsDialog{
		toolsets: toolsets,
		tools:    sortedTools,
	}
	d.readOnlyScrollDialog = newReadOnlyScrollDialog(
		readOnlyScrollDialogSize{widthPercent: 70, minWidth: 60, maxWidth: 100, heightPercent: 80, heightMax: 40},
		d.renderLines,
	)
	return d
}

func (d *toolsDialog) renderLines(contentWidth, _ int) []string {
	title := fmt.Sprintf("Tools (%d toolsets · %d tools)", len(d.toolsets), len(d.tools))
	lines := []string{
		RenderTitle(title, contentWidth, styles.DialogTitleStyle),
		RenderSeparator(contentWidth),
		"",
	}

	lines = append(lines, d.renderToolsets(contentWidth)...)
	lines = append(lines, d.renderTools(contentWidth)...)

	return lines
}

func (d *toolsDialog) renderToolsets(contentWidth int) []string {
	out := []string{sectionHeader("Toolsets"), ""}

	if len(d.toolsets) == 0 {
		out = append(out, "  "+styles.MutedStyle.Render("No toolsets configured."), "")
		return out
	}

	// Align name + state badge into a fixed-width gutter so the Kind
	// label lines up across rows. The gutter is sized to the longest
	// rendered name.
	nameWidth := 0
	for i := range d.toolsets {
		if w := lipgloss.Width(d.toolsets[i].Name); w > nameWidth {
			nameWidth = w
		}
	}

	for i := range d.toolsets {
		out = append(out, formatToolsetStatus(&d.toolsets[i], nameWidth, contentWidth)...)
	}
	out = append(out, "")
	return out
}

func (d *toolsDialog) renderTools(contentWidth int) []string {
	out := []string{sectionHeader("Tools"), ""}

	if len(d.tools) == 0 {
		out = append(out, "  "+styles.MutedStyle.Render("No tools available."), "")
		return out
	}

	var lastCategory string
	for i := range d.tools {
		t := &d.tools[i]
		cat := t.Category
		if cat == "" {
			cat = "Other"
		}
		if cat != lastCategory {
			if lastCategory != "" {
				out = append(out, "")
			}
			out = append(out, "  "+lipgloss.NewStyle().Bold(true).Foreground(styles.TextSecondary).Render(cat))
			lastCategory = cat
		}

		name := lipgloss.NewStyle().Foreground(styles.Highlight).Render("    " + t.DisplayName())
		if desc, _, _ := strings.Cut(t.Description, "\n"); desc != "" {
			separator := " • "
			separatorWidth := lipgloss.Width(separator)
			nameWidth := lipgloss.Width(name)
			availableWidth := contentWidth - nameWidth - separatorWidth
			if availableWidth > 0 {
				truncated := toolcommon.TruncateText(desc, availableWidth)
				name += styles.MutedStyle.Render(separator + truncated)
			}
		}
		out = append(out, name)
	}
	out = append(out, "")

	return out
}

// sectionHeader returns the styled top-level section header used inside
// the dialog ("Toolsets", "Tools"). Kept private to make the dialog
// layout self-contained.
func sectionHeader(label string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(styles.TextSecondary).Render(label)
}

// formatToolsetStatus renders one toolset as a small block:
//
//	NAME         [state]   KIND
//	  last_error: ...                    (only when set, wrapped to width)
//	  restarts: N                        (only when > 0)
//
// nameColWidth is the (rune-width) padding to apply to the name column
// so adjacent rows align their state badge and Kind label. contentWidth
// is the inner width of the dialog; long error messages are wrapped to
// fit (the dialog is scrollable, so the full text is reachable).
func formatToolsetStatus(s *tools.ToolsetStatus, nameColWidth, contentWidth int) []string {
	name := styles.BoldStyle.Render(s.Name)
	if pad := nameColWidth - lipgloss.Width(s.Name); pad > 0 {
		name += strings.Repeat(" ", pad)
	}
	headline := "  " + name + " " + formatStateBadge(s.State)
	if kind := toolsetKindLabel(s.Kind); kind != "" {
		headline += "  " + styles.MutedStyle.Render(kind)
	}
	out := []string{headline}

	if s.LastError != nil {
		out = append(out, formatLastErrorLines(s.LastError.Error(), contentWidth)...)
	}

	if s.RestartCount > 0 {
		out = append(out, "    "+styles.MutedStyle.Render(fmt.Sprintf("restarts: %d", s.RestartCount)))
	}

	return out
}

// formatLastErrorLines renders a "last_error:" block, wrapping the
// message to fit contentWidth so users can read the entire error.
// Continuation lines are indented to align under the message text:
//
//	    last_error: first wrapped portion of the message
//	                continuation aligned under the first chunk
//
// Newlines in the original error are preserved (each line is wrapped
// independently), so multi-line errors like stack traces stay legible.
func formatLastErrorLines(msg string, contentWidth int) []string {
	const indent = "    "
	const label = "last_error: "
	prefixWidth := lipgloss.Width(indent) + lipgloss.Width(label)
	avail := contentWidth - prefixWidth
	if avail < 1 {
		avail = 1
	}

	var chunks []string
	for _, line := range strings.Split(msg, "\n") {
		if line == "" {
			chunks = append(chunks, "")
			continue
		}
		wrapped := ansi.Wrap(line, avail, " /-_,;:")
		chunks = append(chunks, strings.Split(wrapped, "\n")...)
	}

	cont := indent + strings.Repeat(" ", lipgloss.Width(label))
	out := make([]string, 0, len(chunks))
	for i, c := range chunks {
		prefix := cont
		if i == 0 {
			prefix = indent + label
		}
		out = append(out, styles.ErrorStyle.Render(prefix+c))
	}
	return out
}

// toolsetKindLabel returns the user-facing classification for a toolset.
// Empty Kind (toolsets that don't implement tools.Kinder, e.g. the
// built-in memory/shell/filesystem toolsets) is rendered as "Built-in"
// rather than left blank, so every row carries a visible label.
func toolsetKindLabel(kind string) string {
	if kind == "" {
		return "Built-in"
	}
	return kind
}

// formatStateBadge returns a short bracketed label for the lifecycle state,
// styled to draw the eye to non-Ready states.
func formatStateBadge(s lifecycle.State) string {
	label := "[" + s.String() + "]"
	switch s {
	case lifecycle.StateReady:
		return styles.SuccessStyle.Render(label)
	case lifecycle.StateDegraded, lifecycle.StateRestarting, lifecycle.StateStarting:
		return styles.WarningStyle.Render(label)
	case lifecycle.StateFailed:
		return styles.ErrorStyle.Render(label)
	case lifecycle.StateStopped:
		return styles.MutedStyle.Render(label)
	default:
		return label
	}
}
