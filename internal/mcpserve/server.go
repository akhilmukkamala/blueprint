// Package mcpserve is the neutral MCP interface (DESIGN §9): a hand-rolled
// JSON-RPC 2.0 stdio server (newline-delimited, protocol 2025-06-18, no SDK
// dependency) exposing five stable tools: repo_map, search, symbols,
// blast_radius, spec_lookup. Local-first, zero egress. Absent backends
// return honest degraded JSON results, never protocol errors (G14).
package mcpserve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "blueprint"
	serverVersion   = "0.1.0"
)

// JSON-RPC 2.0 error codes used by the loop.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server serves MCP over one reader/writer pair (stdio in production, pipes
// in tests). It is stateless between requests except the repo root.
type Server struct {
	RepoRoot string
}

// New returns a server rooted at repoRoot.
func New(repoRoot string) *Server {
	return &Server{RepoRoot: repoRoot}
}

// Serve reads newline-delimited JSON-RPC messages until EOF. Notifications
// (no id) get no response, per JSON-RPC 2.0.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{Code: codeParseError, Message: fmt.Sprintf("parse error: %v — send one JSON-RPC 2.0 object per line", err)}})
			continue
		}
		resp := s.dispatch(&req)
		if resp == nil {
			continue // notification
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) dispatch(req *request) *response {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	var (
		result any
		rpcErr *rpcError
	)
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": toolList()}
	case "tools/call":
		result, rpcErr = s.callTool(req.Params)
	default:
		rpcErr = &rpcError{Code: codeMethodNotFound,
			Message: fmt.Sprintf("method %q not found — this server implements initialize, ping, tools/list, tools/call", req.Method)}
	}
	if isNotification {
		return nil
	}
	return &response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
}

// callTool unwraps tools/call params and runs the named tool. Tool-level
// problems come back inside the result (isError or degraded payloads);
// only malformed params are protocol errors.
func (s *Server) callTool(params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams,
			Message: "tools/call needs {\"name\": string, \"arguments\": object}"}
	}
	h, ok := toolHandlers[p.Name]
	if !ok {
		return toolError(fmt.Sprintf("unknown tool %q — call tools/list for the five available tools", p.Name)), nil
	}
	payload, callErr := h(s, p.Arguments)
	if callErr != nil {
		return toolError(callErr.Error()), nil
	}
	return toolResult(payload), nil
}

// toolResult wraps a payload as MCP text content holding pretty JSON.
func toolResult(payload any) map[string]any {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return toolError(fmt.Sprintf("internal: result not serializable: %v", err))
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}
