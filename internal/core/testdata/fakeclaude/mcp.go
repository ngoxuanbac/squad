package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
)

// server is the stdio MCP server named squad in --mcp-config.
type server struct {
	Command string
	Args    []string
	Env     map[string]string
}

func squadServer() (server, error) {
	i := slices.Index(os.Args, "--mcp-config")
	if i < 0 || i+1 >= len(os.Args) {
		return server{}, errors.New("no --mcp-config")
	}
	b, err := os.ReadFile(os.Args[i+1])
	if err != nil {
		return server{}, err
	}
	var cfg struct {
		Servers map[string]server `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return server{}, err
	}
	s, ok := cfg.Servers["squad"]
	if !ok {
		return server{}, errors.New("no squad server in --mcp-config")
	}
	return s, nil
}

// mcpClient talks to the squad MCP server, as Claude starts it.
type mcpClient struct {
	in    io.Writer
	out   *bufio.Scanner
	id    int
	tools map[string]schema
}

type schema struct {
	Required   []string
	Properties map[string]struct{ Enum []string }
}

func dialMCP() (*mcpClient, error) {
	s, err := squadServer()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	mark("mcp", cmd.Process.Pid)
	c := &mcpClient{in: in, out: bufio.NewScanner(stdout)}
	c.out.Buffer(make([]byte, 64<<10), 16<<20)
	if _, err := c.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "fakeclaude", "version": "0"}}); err != nil {
		return nil, err
	}
	if err := json.NewEncoder(in).Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return nil, err
	}
	raw, err := c.call("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var list struct {
		Tools []struct {
			Name   string
			Schema schema `json:"inputSchema"`
		}
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	c.tools = map[string]schema{}
	for _, tl := range list.Tools {
		c.tools[tl.Name] = tl.Schema
	}
	return c, nil
}

// check refuses what Claude would not send: a tool it was not told of, an argument missing that
// the tool's schema requires, a value outside its enum.
func (c *mcpClient) check(name string, input json.RawMessage) error {
	s, ok := c.tools[name]
	if !ok {
		return fmt.Errorf("no tool %s", name)
	}
	var args map[string]any
	_ = json.Unmarshal(input, &args)
	for _, r := range s.Required {
		if _, ok := args[r]; !ok {
			return fmt.Errorf("%s: %s is required", name, r)
		}
	}
	for k, v := range args {
		if e := s.Properties[k].Enum; e != nil && !slices.Contains(e, fmt.Sprint(v)) {
			return fmt.Errorf("%s: %s=%v is not one of %v", name, k, v, e)
		}
	}
	return nil
}

func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	c.id++
	if err := json.NewEncoder(c.in).Encode(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for c.out.Scan() {
		var r struct {
			ID     int
			Result json.RawMessage
			Error  *struct{ Message string }
		}
		if json.Unmarshal(c.out.Bytes(), &r) != nil || r.ID != c.id {
			continue
		}
		if r.Error != nil {
			return nil, errors.New(r.Error.Message)
		}
		return r.Result, nil
	}
	return nil, errors.New("the MCP server is gone")
}
