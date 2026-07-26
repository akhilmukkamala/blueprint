// fakegraph is the test double for the Tier-3 graph backend: a minimal MCP
// stdio server compiled on demand by the proxy tests. Modes (first arg):
//
//	answer    (default) answer tools/call with a JSON payload echoing the call
//	toolerror answer tools/call with isError:true content
//	rpcerror  answer tools/call with a JSON-RPC error
//	silent    handshake, then exit before answering tools/call
//	hang      handshake, then block forever (timeout path)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	mode := "answer"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			fmt.Fprintln(os.Stderr, "fakegraph: bad request:", err)
			os.Exit(1)
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fakegraph", "version": "0.0.1"},
			}})
		case "notifications/initialized":
			// silent, per JSON-RPC
		case "tools/call":
			switch mode {
			case "silent":
				return
			case "hang":
				time.Sleep(time.Hour)
			case "rpcerror":
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID),
					"error": map[string]any{"code": -32000, "message": "graph store offline"}})
			case "toolerror":
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "index corrupt"}},
					"isError": true,
				}})
			default:
				payload, _ := json.Marshal(map[string]any{
					"backend": "fakegraph",
					"tool":    req.Params.Name,
					"args":    req.Params.Arguments,
					"nodes":   []string{"auth/login.go", "cmd/main.go"},
				})
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": string(payload)}},
					"isError": false,
				}})
			}
		}
	}
}
