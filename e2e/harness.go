//go:build e2e

// Package e2e drives the data-toolbox-mcp binary via JSON-RPC over stdio,
// simulating a real MCP client. Tests in this package are skipped from
// `go test ./...`; run them with:
//
//	go test -tags e2e ./e2e/...
//
// They also require DATA_TOOLBOX_TEST_PODMAN=1 and a built binary at
// dist/data-toolbox-mcp (or override via DATA_TOOLBOX_TEST_BINARY).
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// Harness drives a spawned `data-toolbox-mcp serve` process.
type Harness struct {
	t        *testing.T
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan []byte
	nextID   atomic.Int64
	stderrLn []byte // last stderr line for diagnostics
}

// Start spawns the binary with `serve --config configPath`, hooks up stdio,
// and registers cleanup so the binary is stopped at test end.
func Start(t *testing.T, binary, configPath string) *Harness {
	t.Helper()

	args := []string{"serve"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	cmd := exec.Command(binary, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	// Inherit stderr to the test's stderr (visible with `go test -v`).
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	lines := make(chan []byte, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			b := make([]byte, len(scanner.Bytes()))
			copy(b, scanner.Bytes())
			lines <- b
		}
	}()

	h := &Harness{t: t, cmd: cmd, stdin: stdin, lines: lines}
	t.Cleanup(h.Close)
	return h
}

// Close gracefully shuts the server down by closing stdin and waiting.
func (h *Harness) Close() {
	_ = h.stdin.Close()
	_ = h.cmd.Wait()
}

// Call sends a JSON-RPC request and waits up to `timeout` for the matching
// response. Returns the response's result or an error (transport, timeout,
// or RPC error).
func (h *Harness) Call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := h.nextID.Add(1)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	if err := json.NewEncoder(h.stdin).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-h.lines:
			if !ok {
				return nil, fmt.Errorf("server stdout closed before response (method=%s id=%d)", method, id)
			}
			var resp struct {
				ID     json.Number     `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(line, &resp); err != nil {
				return nil, fmt.Errorf("parse response: %w (line=%q)", err, string(line))
			}
			respID, err := resp.ID.Int64()
			if err != nil || respID != id {
				// Out-of-order or notification; skip and keep reading.
				continue
			}
			if resp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		case <-deadline:
			return nil, fmt.Errorf("timeout after %v waiting for response (method=%s id=%d)", timeout, method, id)
		}
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (h *Harness) Notify(method string) error {
	return json.NewEncoder(h.stdin).Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	})
}

// CallTool is a convenience helper for tools/call: it returns the inner JSON
// text from the first content block, which is what our tool handlers emit.
func (h *Harness) CallTool(name string, arguments any, timeout time.Duration) (json.RawMessage, bool, error) {
	res, err := h.Call("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, timeout)
	if err != nil {
		return nil, false, err
	}
	var wrap struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return nil, false, fmt.Errorf("parse tools/call result: %w", err)
	}
	if len(wrap.Content) == 0 {
		return nil, wrap.IsError, fmt.Errorf("tools/call returned no content")
	}
	return json.RawMessage(wrap.Content[0].Text), wrap.IsError, nil
}
