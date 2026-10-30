package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// MockClient devolve respostas roteirizadas, em ordem, e registra as
// requisições recebidas. Útil para testar o orquestrador sem rede.
type MockClient struct {
	mu        sync.Mutex
	responses []*Response
	Requests  []Request
}

// NewMock cria um mock com a sequência de respostas informada.
func NewMock(responses ...*Response) *MockClient {
	return &MockClient{responses: responses}
}

func (m *MockClient) Complete(_ context.Context, req Request, onStream StreamFunc) (*Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Copia o slice: o chamador continua anexando ao histórico.
	req.Messages = append([]Message(nil), req.Messages...)
	m.Requests = append(m.Requests, req)
	if len(m.responses) == 0 {
		return nil, fmt.Errorf("mock: sem respostas roteirizadas (chamada %d)", len(m.Requests))
	}
	r := m.responses[0]
	m.responses = m.responses[1:]
	if onStream != nil {
		if r.Message.Text != "" {
			onStream(StreamEvent{Kind: StreamText, Text: r.Message.Text})
		}
		for _, tc := range r.Message.ToolCalls {
			onStream(StreamEvent{Kind: StreamToolStart, ToolName: tc.Name})
		}
	}
	return r, nil
}

// Remaining retorna quantas respostas ainda não foram consumidas.
func (m *MockClient) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.responses)
}

// MockText cria uma resposta final só com texto.
func MockText(text string) *Response {
	return &Response{Message: Message{Role: RoleAssistant, Text: text}, StopReason: StopEndTurn}
}

// MockToolCalls cria uma resposta pedindo ferramentas. inputs é name -> input
// em pares sequenciais: MockToolCalls("pensando", "read_file", map[...]...).
func MockToolCalls(text string, pairs ...any) *Response {
	msg := Message{Role: RoleAssistant, Text: text}
	for i := 0; i+1 < len(pairs); i += 2 {
		raw, err := json.Marshal(pairs[i+1])
		if err != nil {
			panic(err)
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:    fmt.Sprintf("toolu_mock_%d_%d", i/2, len(text)),
			Name:  pairs[i].(string),
			Input: raw,
		})
	}
	return &Response{Message: msg, StopReason: StopToolUse}
}
