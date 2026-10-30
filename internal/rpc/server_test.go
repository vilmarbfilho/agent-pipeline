package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/internal/llm"
	"orchestrator/internal/orchestrator"
	"orchestrator/internal/tools"
)

type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	Params json.RawMessage `json:"params"`
}

func TestServerRunWithApproval(t *testing.T) {
	dir := t.TempDir()
	sb, err := tools.NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan := orchestrator.Plan{Summary: "s", Steps: []orchestrator.PlanStep{{Title: "t", Details: "d"}}}
	mock := llm.NewMock(
		llm.MockToolCalls("", "submit_plan", plan),
		// .env é sensível: exige aprovação via JSON-RPC.
		llm.MockToolCalls("", "write_file", map[string]any{"path": ".env", "content": "A=1"}),
		llm.MockToolCalls("", "finish", orchestrator.CoderReport{Summary: "ok", FilesChanged: []string{".env"}}),
		llm.MockToolCalls("", "submit_review", orchestrator.Review{Approved: true, Summary: "ok", CommitMessage: "chore: env"}),
	)
	srv := NewServer(func(a tools.Approver, s orchestrator.Sink) *orchestrator.Orchestrator {
		return &orchestrator.Orchestrator{LLM: mock, Sink: s, Env: &tools.Env{Sandbox: sb, Approver: a, Policy: tools.DefaultPolicy()}}
	})

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, inR, outW); outW.Close() }()

	send := func(s string) {
		if _, err := io.WriteString(inW, s+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	lines := bufio.NewScanner(outR)
	lines.Buffer(nil, 1<<20)
	next := func() msg {
		t.Helper()
		if !lines.Scan() {
			t.Fatalf("stream encerrado: %v", lines.Err())
		}
		var m msg
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			t.Fatalf("JSON inválido %q: %v", lines.Text(), err)
		}
		return m
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if m := next(); string(m.Result) != `"pong"` {
		t.Fatalf("ping: %+v", m)
	}
	send(`{"jsonrpc":"2.0","id":2,"method":"nope"}`)
	if m := next(); m.Error == nil || m.Error.Code != codeMethodNotFound {
		t.Fatalf("método desconhecido: %+v", m)
	}
	send(`not json`)
	if m := next(); m.Error == nil || m.Error.Code != codeParseError {
		t.Fatalf("parse error: %+v", m)
	}

	send(`{"jsonrpc":"2.0","id":3,"method":"run","params":{"task":"crie .env"}}`)
	var finished bool
	var sawRunReply, sawEvent bool
	for !finished {
		m := next()
		switch m.Method {
		case "":
			sawRunReply = sawRunReply || string(m.ID) == "3"
		case "event":
			sawEvent = true
		case "approval.request":
			var p struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(m.Params, &p)
			send(`{"jsonrpc":"2.0","id":4,"method":"approval.respond","params":{"id":"` + p.ID + `","approved":true}}`)
		case "run.finished":
			var p struct {
				Error  string              `json:"error"`
				Result orchestrator.Result `json:"result"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Error != "" || !p.Result.Success {
				t.Fatalf("run falhou: %+v", p)
			}
			finished = true
		}
	}
	if !sawRunReply || !sawEvent {
		t.Fatalf("faltou resposta do run (%v) ou eventos (%v)", sawRunReply, sawEvent)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".env")); err != nil || string(b) != "A=1" {
		t.Fatalf(".env aprovado deveria existir: %q %v", b, err)
	}
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
