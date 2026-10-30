// Package rpc expõe o orquestrador via JSON-RPC 2.0 sobre um stream
// (stdio por padrão), com mensagens delimitadas por nova linha. É a ponte
// para uma futura UI desktop (Electron/Tauri), que inicia o binário como
// processo filho com `orchestrator serve --repo <dir>`.
//
// Métodos (cliente -> servidor):
//
//	ping                                   -> "pong"
//	run              {task}                -> {run_id}
//	cancel           {run_id}              -> {ok}
//	approval.respond {id, approved}        -> {ok}
//
// Notificações (servidor -> cliente):
//
//	event            {run_id, event}       eventos de streaming/ferramentas
//	approval.request {id, run_id, request} pedido de confirmação humana
//	run.finished     {run_id, result, error}
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"orchestrator/internal/orchestrator"
	"orchestrator/internal/tools"
)

// Factory constrói um orquestrador para uma execução, ligado ao aprovador e
// ao sink fornecidos pelo servidor.
type Factory func(approver tools.Approver, sink orchestrator.Sink) *orchestrator.Orchestrator

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Códigos JSON-RPC padrão.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Server atende um cliente JSON-RPC.
type Server struct {
	factory Factory

	outMu sync.Mutex
	enc   *json.Encoder

	mu        sync.Mutex
	runs      map[string]context.CancelFunc
	approvals map[string]chan bool
	seq       atomic.Int64
	wg        sync.WaitGroup
}

// NewServer cria um servidor.
func NewServer(f Factory) *Server {
	return &Server{factory: f, runs: map[string]context.CancelFunc{}, approvals: map[string]chan bool{}}
}

// Serve lê requisições de r e escreve respostas/notificações em w até EOF ou
// cancelamento do contexto. Execuções em andamento são canceladas ao sair.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.enc = json.NewEncoder(w)
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.reply(json.RawMessage("null"), nil, &rpcError{codeParseError, err.Error()})
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			s.reply(req.ID, nil, &rpcError{codeInvalidRequest, "requisição JSON-RPC 2.0 inválida"})
			continue
		}
		result, rerr := s.dispatch(ctx, req)
		if len(req.ID) > 0 { // notificações do cliente não recebem resposta
			s.reply(req.ID, result, rerr)
		}
	}
	return sc.Err()
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "ping":
		return "pong", nil
	case "run":
		var p struct {
			Task string `json:"task"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Task == "" {
			return nil, &rpcError{codeInvalidParams, "params.task é obrigatório"}
		}
		return map[string]string{"run_id": s.startRun(ctx, p.Task)}, nil
	case "cancel":
		var p struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, err.Error()}
		}
		s.mu.Lock()
		c, ok := s.runs[p.RunID]
		s.mu.Unlock()
		if ok {
			c()
		}
		return map[string]bool{"ok": ok}, nil
	case "approval.respond":
		var p struct {
			ID       string `json:"id"`
			Approved bool   `json:"approved"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, err.Error()}
		}
		s.mu.Lock()
		ch, ok := s.approvals[p.ID]
		delete(s.approvals, p.ID)
		s.mu.Unlock()
		if ok {
			ch <- p.Approved
		}
		return map[string]bool{"ok": ok}, nil
	default:
		return nil, &rpcError{codeMethodNotFound, "método desconhecido: " + req.Method}
	}
}

func (s *Server) startRun(parent context.Context, task string) string {
	runID := fmt.Sprintf("run-%d", s.seq.Add(1))
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.runs[runID] = cancel
	s.mu.Unlock()

	sink := orchestrator.SinkFunc(func(e orchestrator.Event) {
		s.notify("event", map[string]any{"run_id": runID, "event": e})
	})
	approver := tools.ApproverFunc(func(ctx context.Context, req tools.ApprovalRequest) (bool, error) {
		return s.askApproval(ctx, runID, req)
	})
	orch := s.factory(approver, sink)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		res, err := orch.Run(ctx, task)
		params := map[string]any{"run_id": runID, "result": res}
		if err != nil {
			params["error"] = err.Error()
		}
		s.notify("run.finished", params)
		s.mu.Lock()
		delete(s.runs, runID)
		s.mu.Unlock()
	}()
	return runID
}

func (s *Server) askApproval(ctx context.Context, runID string, req tools.ApprovalRequest) (bool, error) {
	id := fmt.Sprintf("appr-%d", s.seq.Add(1))
	ch := make(chan bool, 1)
	s.mu.Lock()
	s.approvals[id] = ch
	s.mu.Unlock()
	s.notify("approval.request", map[string]any{"id": id, "run_id": runID, "request": req})
	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.approvals, id)
		s.mu.Unlock()
		return false, errors.Join(errors.New("aprovação cancelada"), ctx.Err())
	}
}

func (s *Server) reply(id json.RawMessage, result any, err *rpcError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	s.write(response{JSONRPC: "2.0", ID: id, Result: result, Error: err})
}

func (s *Server) notify(method string, params any) {
	s.write(notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *Server) write(v any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	// Sem canal de erro para o cliente: se a escrita falhar, o cliente caiu
	// e o Serve termina no próximo read.
	_ = s.enc.Encode(v)
}
