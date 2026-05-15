package dialog

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/lifecycle"
)

var utf8Valid = utf8.Valid

func TestNewToolsDialog_EmptyShowsBothPlaceholders(t *testing.T) {
	t.Parallel()
	d := NewToolsDialog(nil, nil).(*toolsDialog)
	out := strings.Join(d.renderLines(80, 24), "\n")
	assert.Contains(t, out, "Tools (0 toolsets · 0 tools)")
	assert.Contains(t, out, "No toolsets configured")
	assert.Contains(t, out, "No tools available")
}

func TestNewToolsDialog_RendersToolsetSection(t *testing.T) {
	t.Parallel()

	statuses := []tools.ToolsetStatus{
		{
			Name:  "gopls",
			Kind:  "LSP",
			State: lifecycle.StateReady,
		},
		{
			Name:         "github-mcp",
			Kind:         "Remote MCP",
			State:        lifecycle.StateRestarting,
			LastError:    errors.New("connection reset"),
			RestartCount: 2,
		},
	}
	d := NewToolsDialog(statuses, nil).(*toolsDialog)
	out := strings.Join(d.renderLines(80, 24), "\n")
	assert.Contains(t, out, "Tools (2 toolsets · 0 tools)")
	assert.Contains(t, out, "gopls")
	assert.Contains(t, out, "ready")
	assert.Contains(t, out, "LSP")
	assert.Contains(t, out, "github-mcp")
	assert.Contains(t, out, "restarting")
	assert.Contains(t, out, "Remote MCP")
	assert.Contains(t, out, "connection reset")
	assert.Contains(t, out, "restarts: 2")
}

// TestNewToolsDialog_NoKindRendersBuiltInLabel guards against blank Kind
// rows leaving a hole where the label should be: built-in toolsets
// (memory, shell, filesystem, …) don't implement tools.Kinder, but they
// still need a visible label in the column.
func TestNewToolsDialog_NoKindRendersBuiltInLabel(t *testing.T) {
	t.Parallel()
	statuses := []tools.ToolsetStatus{{
		Name:  "memory",
		State: lifecycle.StateReady,
	}}
	d := NewToolsDialog(statuses, nil).(*toolsDialog)
	out := strings.Join(d.renderLines(80, 24), "\n")
	assert.Contains(t, out, "Built-in")
}

// TestNewToolsDialog_RendersToolsByCategory verifies that the lower
// "Tools" section groups items by their Category and shows the
// per-tool description suffix. The exact rendering is theme-dependent
// so we only assert on the substrings the user actually reads.
func TestNewToolsDialog_RendersToolsByCategory(t *testing.T) {
	t.Parallel()
	toolList := []tools.Tool{
		{Name: "fs_read", Category: "filesystem", Description: "Read a file"},
		{Name: "fs_write", Category: "filesystem", Description: "Write a file"},
		{Name: "shell", Category: "shell", Description: "Execute commands"},
	}
	d := NewToolsDialog(nil, toolList).(*toolsDialog)
	out := strings.Join(d.renderLines(80, 24), "\n")
	assert.Contains(t, out, "Tools (0 toolsets · 3 tools)")
	assert.Contains(t, out, "filesystem")
	assert.Contains(t, out, "shell")
	assert.Contains(t, out, "fs_read")
	assert.Contains(t, out, "Read a file")
	// Category headings should come before their tools in the buffer.
	assert.Less(t, strings.Index(out, "filesystem"), strings.Index(out, "fs_read"))
}

// TestFormatToolsetStatus_WrapsLongErrors guards against silent
// truncation of long error messages: the full text must appear in the
// rendered output, wrapped across multiple lines that fit the dialog
// width (the dialog is scrollable, so the user can reach all of it).
func TestFormatToolsetStatus_WrapsLongErrors(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 1000)

	lines := formatLastErrorLines(long, 80)

	// Concatenating the message content across the wrapped lines must
	// yield the full original message — no characters are dropped.
	var reassembled strings.Builder
	for _, l := range lines {
		reassembled.WriteString(strings.TrimSpace(stripErrorPrefix(l)))
	}
	assert.Equal(t, long, reassembled.String())

	// Each rendered line must fit within the requested content width.
	for _, l := range lines {
		assert.LessOrEqual(t, lipgloss.Width(l), 80, "line wider than content width: %q", l)
	}

	// More than one line: the input is far too long to fit on one row.
	assert.Greater(t, len(lines), 1, "long error must wrap onto multiple lines")
}

// stripErrorPrefix removes the "last_error: " label or its alignment
// padding so the test can recover the raw message content from a
// rendered line. The error style adds ANSI escapes; strip those first.
func stripErrorPrefix(line string) string {
	// Drop ANSI escapes the error style adds around the text.
	stripped := stripANSI(line)
	const label = "last_error: "
	if idx := strings.Index(stripped, label); idx >= 0 {
		return stripped[idx+len(label):]
	}
	return strings.TrimLeft(stripped, " ")
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 0x40 && r <= 0x7e) && r != '[' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestFormatToolsetStatus_WrapsAtRuneBoundary guards against
// byte-based slicing of multi-byte UTF-8 sequences (each emoji is
// 4 bytes; a byte-slicing wrap would land mid-codepoint and produce
// invalid UTF-8). Every byte of the output must remain valid UTF-8.
func TestFormatToolsetStatus_WrapsAtRuneBoundary(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("\U0001F600", 1000) // 1000 "😀" runes (4 bytes each)
	statuses := []tools.ToolsetStatus{{
		Name:      "x",
		State:     lifecycle.StateFailed,
		LastError: errors.New(long),
	}}
	d := NewToolsDialog(statuses, nil).(*toolsDialog)
	out := strings.Join(d.renderLines(80, 24), "\n")
	assert.True(t, utf8ValidString(out), "wrapped output must remain valid UTF-8")
	assert.Equal(t, 1000, strings.Count(out, "\U0001F600"), "every emoji must appear in the wrapped output")
}

// TestFormatToolsetStatus_PreservesNewlinesInError ensures multi-line
// errors (e.g. stack traces) keep their structure instead of being
// flattened into a single line.
func TestFormatToolsetStatus_PreservesNewlinesInError(t *testing.T) {
	t.Parallel()
	statuses := []tools.ToolsetStatus{{
		Name:      "x",
		State:     lifecycle.StateFailed,
		LastError: errors.New("connection refused\n  at dial tcp 127.0.0.1:5000\n  caused by: i/o timeout"),
	}}
	d := NewToolsDialog(statuses, nil).(*toolsDialog)
	out := strings.Join(d.renderLines(80, 24), "\n")
	assert.Contains(t, out, "connection refused")
	assert.Contains(t, out, "at dial tcp 127.0.0.1:5000")
	assert.Contains(t, out, "caused by: i/o timeout")
}

func utf8ValidString(s string) bool {
	return utf8Valid([]byte(s))
}
