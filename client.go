package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// VyOS CLI tool paths
const (
	cliShellAPI  = "/bin/cli-shell-api"
	mySet        = "/opt/vyatta/sbin/my_set"
	myDelete     = "/opt/vyatta/sbin/my_delete"
	myCommit     = "/opt/vyatta/sbin/my_commit"
	configMgmt   = "/usr/bin/config-mgmt"
	opCmdWrapper = "/opt/vyatta/bin/vyatta-op-cmd-wrapper"
	saveConfigPy = "/usr/libexec/vyos/vyos-save-config.py"
	configToJSON = "/usr/bin/vyos-config-to-json"
	mtrExecute   = "/usr/libexec/vyos/op_mode/mtr_execute.py"
	configBoot   = "/config/config.boot"
)

// VyosClient executes VyOS commands directly via CLI tools.
// No REST API, no network, no API key needed.
type VyosClient struct {
	sessionEnv []string
	configMu   sync.Mutex // serializes config-modifying operations
}

func NewVyosClient() (*VyosClient, error) {
	pid := os.Getpid()

	// Get session environment from cli-shell-api
	out, err := exec.Command(cliShellAPI, "getSessionEnv", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, fmt.Errorf("getSessionEnv: %w", err)
	}

	// Start with current environment
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			envMap[e[:i]] = e[i+1:]
		}
	}

	// Parse session env output (format: VAR=value; or declare -x VAR=value)
	re := regexp.MustCompile(`([A-Z_]+)=(/[^;\s]+)`)
	for _, match := range re.FindAllStringSubmatch(string(out), -1) {
		envMap[match[1]] = match[2]
	}

	// Inject VyOS-specific environment (same as configsession.py inject_vyos_env)
	envMap["VYATTA_CFG_GROUP_NAME"] = "vyattacfg"
	envMap["VYATTA_USER_LEVEL_DIR"] = "/opt/vyatta/etc/shell/level/admin"
	envMap["VYATTA_PROCESS_CLIENT"] = "gui2_rest"
	envMap["VYOS_HEADLESS_CLIENT"] = "vyos_mcp_go"
	envMap["vyatta_bindir"] = "/opt/vyatta/bin"
	envMap["vyatta_cfg_templates"] = "/opt/vyatta/share/vyatta-cfg/templates"
	envMap["vyatta_configdir"] = "/opt/vyatta/config"
	envMap["vyatta_datadir"] = "/opt/vyatta/share"
	envMap["vyatta_datarootdir"] = "/opt/vyatta/share"
	envMap["vyatta_libdir"] = "/opt/vyatta/lib"
	envMap["vyatta_libexecdir"] = "/opt/vyatta/libexec"
	envMap["vyatta_op_templates"] = "/opt/vyatta/share/vyatta-op/templates"
	envMap["vyatta_prefix"] = "/opt/vyatta"
	envMap["vyatta_sbindir"] = "/opt/vyatta/sbin"
	envMap["vyatta_sysconfdir"] = "/opt/vyatta/etc"
	envMap["vyos_bin_dir"] = "/usr/bin"
	envMap["vyos_cfg_templates"] = "/opt/vyatta/share/vyatta-cfg/templates"
	envMap["vyos_completion_dir"] = "/usr/libexec/vyos/completion"
	envMap["vyos_configdir"] = "/opt/vyatta/config"
	envMap["vyos_conf_scripts_dir"] = "/usr/libexec/vyos/conf_mode"
	envMap["vyos_datadir"] = "/opt/vyatta/share"
	envMap["vyos_datarootdir"] = "/opt/vyatta/share"
	envMap["vyos_libdir"] = "/opt/vyatta/lib"
	envMap["vyos_libexec_dir"] = "/usr/libexec/vyos"
	envMap["vyos_op_scripts_dir"] = "/usr/libexec/vyos/op_mode"
	envMap["vyos_op_templates"] = "/opt/vyatta/share/vyatta-op/templates"
	envMap["vyos_prefix"] = "/opt/vyatta"
	envMap["vyos_sbin_dir"] = "/usr/sbin"
	envMap["vyos_validators_dir"] = "/usr/libexec/vyos/validators"
	envMap["_OFR_CONFIGURE"] = "ok"

	envMap["SESSION_PID"] = strconv.Itoa(pid)
	envMap["COMMIT_VIA"] = "vyos-mcp-go"

	// Build env slice
	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}

	c := &VyosClient{sessionEnv: envSlice}

	// Setup the config session
	if err := c.runSilent(context.Background(), cliShellAPI, "setupSession"); err != nil {
		return nil, fmt.Errorf("setupSession: %w", err)
	}

	return c, nil
}

// Close tears down the config session.
func (c *VyosClient) Close() {
	c.runSilent(context.Background(), cliShellAPI, "teardownSession")
}

// run executes a command with the session environment and returns stdout.
func (c *VyosClient) run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = c.sessionEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(stdout.String())
		}
		if errMsg != "" {
			return "", fmt.Errorf("%s: %s", name, errMsg)
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return stdout.String(), nil
}

// runSilent executes a command, discarding output.
func (c *VyosClient) runSilent(ctx context.Context, name string, args ...string) error {
	_, err := c.run(ctx, name, args...)
	return err
}

// --- Config reads ---

func (c *VyosClient) ShowConfig(ctx context.Context, path []string, format string) (any, error) {
	if format == "json" {
		return c.showConfigJSON(ctx, path)
	}
	// Raw format: cli-shell-api showConfig <path>
	out, err := c.run(ctx, cliShellAPI, append([]string{"showConfig"}, path...)...)
	if err != nil {
		return nil, err
	}
	return strings.TrimRight(out, "\n"), nil
}

func (c *VyosClient) showConfigJSON(ctx context.Context, path []string) (any, error) {
	// Convert config.boot to JSON
	out, err := c.run(ctx, configToJSON, configBoot)
	if err != nil {
		return nil, fmt.Errorf("config-to-json: %w", err)
	}

	var config any
	if err := json.Unmarshal([]byte(out), &config); err != nil {
		return nil, fmt.Errorf("parse config JSON: %w", err)
	}

	// Navigate to the requested path
	current := config
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path not found: %s", strings.Join(path, " "))
		}
		current, ok = m[key]
		if !ok {
			return nil, fmt.Errorf("path not found: %s", strings.Join(path, " "))
		}
	}
	return current, nil
}

func (c *VyosClient) ConfigExists(ctx context.Context, path []string) (bool, error) {
	cmd := exec.CommandContext(ctx, cliShellAPI, append([]string{"existsActive"}, path...)...)
	cmd.Env = c.sessionEnv
	err := cmd.Run()
	if err != nil {
		// Exit code 1 means path doesn't exist (not an error)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *VyosClient) ReturnValues(ctx context.Context, path []string) (any, error) {
	out, err := c.run(ctx, cliShellAPI, append([]string{"returnActiveValues"}, path...)...)
	if err != nil {
		return nil, err
	}
	// Output is space-separated single-quoted values: 'val1' 'val2'
	// Values may contain spaces, so we parse quoted tokens explicitly.
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return []string{}, nil
	}
	return parseQuotedValues(trimmed), nil
}

// parseQuotedValues parses VyOS returnValues output: space-separated
// single-quoted tokens, e.g. 'val1' 'val with spaces' 'val3'.
func parseQuotedValues(s string) []string {
	var values []string
	for len(s) > 0 {
		if s[0] != '\'' {
			break
		}
		idx := strings.IndexByte(s[1:], '\'')
		if idx < 0 {
			// Malformed: unclosed quote — return remainder stripped of the leading quote
			values = append(values, s[1:])
			break
		}
		values = append(values, s[1:idx+1])
		s = strings.TrimSpace(s[idx+2:])
	}
	return values
}

// --- Config writes (serialized) ---

func (c *VyosClient) SetConfig(ctx context.Context, path []string) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	return c.runSilent(ctx, mySet, path...)
}

func (c *VyosClient) BatchConfigure(ctx context.Context, operations []map[string]any) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	for _, op := range operations {
		opStr, _ := op["op"].(string)
		pathAny, _ := op["path"].([]any)
		path := make([]string, len(pathAny))
		for i, p := range pathAny {
			path[i], _ = p.(string)
		}
		var cmd string
		switch opStr {
		case "set":
			cmd = mySet
		case "delete":
			cmd = myDelete
		default:
			return fmt.Errorf("unknown operation: %s", opStr)
		}
		if err := c.runSilent(ctx, cmd, path...); err != nil {
			return err
		}
	}
	return nil
}

func (c *VyosClient) DeleteConfig(ctx context.Context, path []string) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	return c.runSilent(ctx, myDelete, path...)
}

func (c *VyosClient) Commit(ctx context.Context, comment *string, confirmTimeout *int) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()

	// Always commit first
	if err := c.runSilent(ctx, myCommit); err != nil {
		return err
	}

	// If commit-confirm requested, schedule auto-rollback timer
	if confirmTimeout != nil && *confirmTimeout > 0 {
		minutes := *confirmTimeout
		action := `sg vyattacfg "/usr/bin/config-mgmt revert_soft"`
		timerCmd := fmt.Sprintf("systemd-run --quiet --on-active=%dm --unit=commit-confirm %s", minutes, action)
		cmd := exec.CommandContext(ctx, "sudo", "bash", "-c", timerCmd)
		cmd.Env = c.sessionEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to set rollback timer: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	return nil
}

// Confirm locks in a pending commit-confirm, cancelling the auto-rollback.
func (c *VyosClient) Confirm(ctx context.Context) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()

	// Stop the rollback timer
	cmd := exec.CommandContext(ctx, "sudo", "systemctl", "stop", "--quiet", "commit-confirm.timer")
	cmd.Env = c.sessionEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("no confirm pending or failed to stop timer: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Kill notification script if running
	killCmd := exec.CommandContext(ctx, "sudo", "pkill", "-f", "commit-confirm-notify.py")
	killCmd.Env = c.sessionEnv
	_ = killCmd.Run() // ignore error if not running

	return nil
}

func (c *VyosClient) Save(ctx context.Context) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	return c.runSilent(ctx, saveConfigPy, configBoot)
}

// --- Operational commands ---

func (c *VyosClient) Show(ctx context.Context, path []string) (any, error) {
	out, err := c.run(ctx, opCmdWrapper, append([]string{"show"}, path...)...)
	if err != nil {
		return nil, err
	}
	return strings.TrimRight(out, "\n"), nil
}

func (c *VyosClient) Reset(ctx context.Context, path []string) (any, error) {
	out, err := c.run(ctx, opCmdWrapper, append([]string{"reset"}, path...)...)
	if err != nil {
		return nil, err
	}
	return strings.TrimRight(out, "\n"), nil
}

func (c *VyosClient) Generate(ctx context.Context, path []string) (any, error) {
	out, err := c.run(ctx, opCmdWrapper, append([]string{"generate"}, path...)...)
	if err != nil {
		return nil, err
	}
	return strings.TrimRight(out, "\n"), nil
}

func (c *VyosClient) Ping(ctx context.Context, host string, count int) (any, error) {
	out, err := c.run(ctx, opCmdWrapper, "ping", host, "count", strconv.Itoa(count))
	if err != nil {
		return nil, err
	}
	return strings.TrimRight(out, "\n"), nil
}

// --- Diagnostics ---

func (c *VyosClient) Traceroute(ctx context.Context, host string) (any, error) {
	out, err := c.run(ctx, mtrExecute, "mtr",
		"--for-api", "--report-mode", "--report-cycles", "1", "--json", "--host", host)
	if err != nil {
		return nil, err
	}
	// mtr outputs JSON
	var result any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return strings.TrimRight(out, "\n"), nil
	}
	return result, nil
}
