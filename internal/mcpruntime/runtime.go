package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"

	"ternura/tool"
)

const defaultConfigPath = ".ternura/mcp.json"

type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

type ServerConfig struct {
	Transport       string            `json:"transport"`
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	URL             string            `json:"url,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Tools           []string          `json:"tools,omitempty"`
	RequireApproval *bool             `json:"require_approval,omitempty"`
}

type Runtime struct {
	tools   []tool.Tool
	clients []client.MCPClient
}

func ConfigPathFromEnv() string {
	if path := strings.TrimSpace(os.Getenv("TERNURA_MCP_CONFIG")); path != "" {
		return path
	}
	return defaultConfigPath
}

func Load(ctx context.Context, path string) (*Runtime, error) {
	runtime := &Runtime{}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return runtime, nil
	}
	if err != nil {
		return runtime, err
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return runtime, fmt.Errorf("parse MCP config %s: %w", path, err)
	}

	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var loadErrors []error
	for _, name := range names {
		if err := runtime.loadServer(ctx, name, config.Servers[name]); err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("MCP server %s: %w", name, err))
		}
	}
	return runtime, errors.Join(loadErrors...)
}

func (r *Runtime) Tools() []tool.Tool {
	if r == nil {
		return nil
	}
	return append([]tool.Tool(nil), r.tools...)
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var closeErrors []error
	for _, cli := range r.clients {
		if err := cli.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

func (r *Runtime) loadServer(ctx context.Context, serverName string, config ServerConfig) error {
	cli, err := connect(ctx, config)
	if err != nil {
		return err
	}
	initialized := false
	defer func() {
		if !initialized {
			_ = cli.Close()
		}
	}()

	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request := protocol.InitializeRequest{}
	request.Params.ProtocolVersion = protocol.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = protocol.Implementation{Name: "ternura", Version: "1"}
	if _, err := cli.Initialize(initCtx, request); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	bases, err := einomcp.GetTools(initCtx, &einomcp.Config{
		Cli:           cli,
		ToolNameList:  config.Tools,
		CustomHeaders: expandMap(config.Headers),
	})
	if err != nil {
		return err
	}
	requireApproval := true
	if config.RequireApproval != nil {
		requireApproval = *config.RequireApproval
	}
	for _, base := range bases {
		info, err := base.Info(initCtx)
		if err != nil {
			return err
		}
		exposedName := "mcp_" + sanitizeName(serverName) + "_" + sanitizeName(info.Name)
		adapted, err := tool.AdaptEinoTool(
			initCtx,
			exposedName,
			base,
			requireApproval,
			fmt.Sprintf("MCP tool %s on server %s can affect an external system", info.Name, serverName),
		)
		if err != nil {
			return err
		}
		r.tools = append(r.tools, adapted)
	}
	r.clients = append(r.clients, cli)
	initialized = true
	return nil
}

func connect(ctx context.Context, config ServerConfig) (client.MCPClient, error) {
	switch strings.ToLower(strings.TrimSpace(config.Transport)) {
	case "stdio", "":
		command := os.ExpandEnv(strings.TrimSpace(config.Command))
		if command == "" {
			return nil, errors.New("stdio command is required")
		}
		env := os.Environ()
		for key, value := range expandMap(config.Env) {
			env = append(env, key+"="+value)
		}
		args := make([]string, len(config.Args))
		for idx, arg := range config.Args {
			args[idx] = os.ExpandEnv(arg)
		}
		return client.NewStdioMCPClient(command, env, args...)
	case "http", "streamable_http", "streamable-http":
		url := os.ExpandEnv(strings.TrimSpace(config.URL))
		if url == "" {
			return nil, errors.New("HTTP URL is required")
		}
		cli, err := client.NewStreamableHttpClient(url, transport.WithHTTPHeaders(expandMap(config.Headers)))
		if err != nil {
			return nil, err
		}
		if err := cli.Start(ctx); err != nil {
			_ = cli.Close()
			return nil, err
		}
		return cli, nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", config.Transport)
	}
}

func expandMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	expanded := make(map[string]string, len(values))
	for key, value := range values {
		expanded[key] = os.ExpandEnv(value)
	}
	return expanded
}

var invalidToolNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeName(name string) string {
	name = invalidToolNameChars.ReplaceAllString(strings.TrimSpace(name), "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "tool"
	}
	return name
}

func ResolveConfigPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absolute
}
