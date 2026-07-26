// Tier-3 graph proxy (DESIGN §9): when [retrieval.graph].command is set,
// blast_radius and search{mode:"graph"} spawn the configured command as an
// MCP stdio subprocess, forward the single tools/call, translate the reply,
// and kill the process. The backend is exec'd, never linked — the blueprint
// binary itself stays network-incapable — and every failure degrades
// honestly to Tier 1 with the error named (G14), never a protocol error.
package mcpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"blueprint/internal/retrieval"
)

// graphTimeout bounds one proxied call, spawn to reply. A variable so tests
// can shorten it; production is the documented 30s.
var graphTimeout = 30 * time.Second

// graphCall runs one tools/call against the external backend: spawn,
// initialize handshake, forward, read the reply, kill. The command line is
// whitespace-split (no shell — Windows-clean).
func graphCall(command, tool string, args map[string]any) (any, error) {
	argv := retrieval.SplitCommand(command)
	if len(argv) == 0 {
		return nil, fmt.Errorf("graph command is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), graphTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start graph backend %q: %v", command, err)
	}
	// The subprocess lives exactly one call: kill + reap on every exit path.
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	enc := json.NewEncoder(stdin)
	for _, req := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}},
	} {
		if err := enc.Encode(req); err != nil {
			return nil, fmt.Errorf("cannot write to graph backend %q: %v", command, err)
		}
	}

	// On timeout CommandContext kills the child, its stdout closes, and the
	// read loop falls through — no separate watchdog needed.
	dec := json.NewDecoder(stdout)
	for {
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := dec.Decode(&resp); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("graph backend %q timed out after %s", command, graphTimeout)
			}
			return nil, fmt.Errorf("graph backend %q ended without answering tools/call: %v", command, err)
		}
		if string(bytes.TrimSpace(resp.ID)) != "2" {
			continue // initialize reply or unsolicited notification
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("graph backend %q returned error %d: %s", command, resp.Error.Code, resp.Error.Message)
		}
		return translateToolReply(command, resp.Result)
	}
}

// translateToolReply unwraps the MCP content envelope into our payload
// shape: the first text content item, decoded as JSON when it parses, kept
// as plain text otherwise. A reply without the envelope passes through
// verbatim so richer backends are not flattened.
func translateToolReply(command string, raw json.RawMessage) (any, error) {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Content) == 0 {
		var v any
		if err2 := json.Unmarshal(raw, &v); err2 != nil || v == nil {
			return nil, fmt.Errorf("graph backend %q reply is not decodable MCP content", command)
		}
		return v, nil
	}
	if res.IsError {
		return nil, fmt.Errorf("graph backend %q reported a tool error: %s", command, res.Content[0].Text)
	}
	var v any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &v); err == nil {
		return v, nil
	}
	return res.Content[0].Text, nil
}

// graphConfig loads the retrieval config for one call. The error is folded
// into (Config{}, reason) because a broken config.toml must degrade the
// answer, not turn a retrieval tool into a hard failure.
func graphConfig(s *Server) (retrieval.Config, string) {
	cfg, err := retrieval.Load(s.RepoRoot)
	if err != nil {
		return retrieval.Config{}, err.Error()
	}
	return cfg, ""
}

// tierConfigRemediation names the exact config keys that enable Tier 2/3 —
// the degraded message is the installation manual (G14).
const tierConfigRemediation = "enable Tier 2 in .blueprint/config.toml ([retrieval] tier2_packing = \"repomix\" and/or tier2_lsp = \"serena\") then run `blueprint sync`; " +
	"for Tier-3 graph queries set [retrieval.graph] command = \"<mcp-stdio-command>\"; verify binaries with `blueprint doctor`"
