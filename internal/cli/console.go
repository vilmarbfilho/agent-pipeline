package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"orchestrator/internal/orchestrator"
	"orchestrator/internal/tools"
)

// Console renderiza eventos no terminal (modo "pretty") e implementa a
// confirmação humana via stdin. Um único mutex serializa saída e prompts para
// que o streaming do LLM não se misture com a pergunta de confirmação.
type Console struct {
	mu        sync.Mutex
	out       io.Writer
	in        *bufio.Reader
	color     bool
	jsonMode  bool
	autoYes   bool
	verbose   bool
	midStream bool // última escrita foi um delta sem quebra de linha
	lastKind  orchestrator.EventType
}

// NewConsole cria o console.
func NewConsole(out io.Writer, in io.Reader, jsonMode, autoYes, verbose bool) *Console {
	return &Console{
		out: out, in: bufio.NewReader(in), jsonMode: jsonMode, autoYes: autoYes, verbose: verbose,
		color: os.Getenv("NO_COLOR") == "" && isTerminal(out),
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

const (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cBold   = "\033[1m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cBlue   = "\033[34m"
	cCyan   = "\033[36m"
)

func (c *Console) paint(code, s string) string {
	if !c.color {
		return s
	}
	return code + s + cReset
}

// Emit implementa orchestrator.Sink.
func (c *Console) Emit(e orchestrator.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jsonMode {
		b, _ := json.Marshal(e)
		fmt.Fprintln(c.out, string(b))
		return
	}
	// Quebra de linha ao alternar entre thinking/texto/outros.
	if c.midStream && e.Type != c.lastKind {
		fmt.Fprintln(c.out)
		c.midStream = false
	}
	c.lastKind = e.Type

	switch e.Type {
	case orchestrator.EventRunStarted:
		fmt.Fprintf(c.out, "%s %s\n", c.paint(cBold, "▶ Tarefa:"), e.Text)
	case orchestrator.EventPhaseStarted:
		fmt.Fprintf(c.out, "\n%s\n", c.paint(cBold+cBlue, "══ "+strings.ToUpper(e.Phase)+" ══"))
	case orchestrator.EventLLMThinking:
		fmt.Fprint(c.out, c.paint(cDim, e.Text))
		c.midStream = true
	case orchestrator.EventLLMText:
		fmt.Fprint(c.out, e.Text)
		c.midStream = true
	case orchestrator.EventLLMTurnEnd:
		if c.verbose && e.Usage != nil {
			fmt.Fprintf(c.out, "%s\n", c.paint(cDim, fmt.Sprintf("  [passo %d · %s · in=%d out=%d cache=%d]",
				e.Step, e.Text, e.Usage.InputTokens, e.Usage.OutputTokens, e.Usage.CacheReadTokens)))
		}
	case orchestrator.EventToolCall:
		fmt.Fprintf(c.out, "%s %s\n", c.paint(cCyan, "→ "+e.Tool), summarizeInput(e.Input))
	case orchestrator.EventToolResult:
		color, mark := cGreen, "✓"
		if e.IsError {
			color, mark = cRed, "✗"
		}
		fmt.Fprintf(c.out, "%s\n", c.paint(color, "  "+mark+" "+preview(e.Output, c.verbose)))
	case orchestrator.EventPhaseCompleted:
		fmt.Fprintf(c.out, "%s\n", c.paint(cGreen, fmt.Sprintf("✔ %s concluído em %d passo(s)", e.Phase, e.Step)))
		if p, ok := e.Data.(orchestrator.Plan); ok {
			fmt.Fprintf(c.out, "\n%s\n%s\n", c.paint(cBold, "Plano:"), p.Markdown())
		}
	case orchestrator.EventError:
		fmt.Fprintf(c.out, "%s\n", c.paint(cRed, "✗ "+e.Text))
	case orchestrator.EventRunCompleted:
		// O resumo final é impresso pelo comando.
	}
}

// Approve implementa tools.Approver perguntando no terminal.
func (c *Console) Approve(_ context.Context, req tools.ApprovalRequest) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.midStream {
		fmt.Fprintln(c.out)
		c.midStream = false
	}
	w := c.out
	if c.jsonMode {
		w = os.Stderr // mantém stdout como JSONL puro
	}
	fmt.Fprintf(w, "\n%s %s\n  motivo: %s\n", c.paint(cYellow+cBold, "⚠ Confirmação necessária:"), req.Action, req.Reason)
	if req.Details != "" && req.Details != strings.TrimPrefix(req.Action, req.Tool+" ") {
		fmt.Fprintf(w, "%s\n", c.paint(cDim, indent(headLines(req.Details, 20), "  │ ")))
	}
	if c.autoYes {
		fmt.Fprintln(w, c.paint(cYellow, "  aprovado automaticamente (--yes)"))
		return true, nil
	}
	fmt.Fprint(w, "  Permitir? [y/N] ")
	line, err := c.in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(w, "\n  (sem entrada interativa; negado)")
		return false, nil
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes" || ans == "s" || ans == "sim", nil
}

func summarizeInput(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	var parts []string
	for _, k := range []string{"path", "pattern", "command", "args", "start_line", "end_line"} {
		if v, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if c, ok := m["content"].(string); ok {
		parts = append(parts, fmt.Sprintf("content=<%d bytes>", len(c)))
	}
	if len(parts) == 0 && len(raw) < 200 {
		return string(raw)
	}
	return strings.Join(parts, " ")
}

func preview(s string, long bool) string {
	n := 3
	if long {
		n = 20
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n    ") + fmt.Sprintf("\n    … (+%d linhas)", len(lines)-n)
	}
	return strings.Join(lines, "\n    ")
}

func headLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… (+%d linhas)", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
