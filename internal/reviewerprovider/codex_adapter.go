package reviewerprovider

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

const codexReviewerLoopbackBaseURLEnvironment = "GENTLE_AI_CODEX_REVIEWER_LOOPBACK_BASE_URL"

const codexReviewerLoopbackProviderID = "gentle_ai_reviewer_loopback"

// CodexAdapter invokes Codex with an opaque provider invocation and returns
// the CLI's raw final-message bytes without interpreting them.
type CodexAdapter struct {
	Model          string
	LookPath       func(string) (string, error)
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// NewCodexAdapter returns an adapter using the Codex binary resolved from PATH.
func NewCodexAdapter() *CodexAdapter {
	return &CodexAdapter{LookPath: exec.LookPath, commandContext: exec.CommandContext}
}

// Review runs Codex in an empty temporary directory. The prompt is delivered
// through stdin so command arguments never carry provider material. When the
// invocation offers a probe workspace (the S11 refuter), Codex instead runs in
// a candidate copy inside that temporary directory with workspace-write
// confined to it and no network; a temp dir that resolves inside the reviewed
// source root is refused before anything is created. The whole directory is
// removed on return, including on error and timeout.
func (adapter *CodexAdapter) Review(ctx context.Context, invocation Invocation) ([]byte, error) {
	lookPath := adapter.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: %w", err)
	}

	probe := invocation.ProbeWorkspace()
	scratchParent := ""
	if probe != nil {
		if scratchParent, err = probeScratchParent(invocation.ProbeSourceRoot()); err != nil {
			return nil, fmt.Errorf("codex reviewer transport unavailable: %w", err)
		}
	}
	scratch, err := os.MkdirTemp(scratchParent, "gentle-ai-codex-reviewer-*")
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: create scratch directory: %w", err)
	}
	defer removeProbeScratch(scratch)

	workdir := filepath.Join(scratch, "candidate")
	if err := os.Mkdir(workdir, 0o700); err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: create candidate workspace: %w", err)
	}
	if probe != nil {
		if err := probe(ctx, workdir); err != nil {
			return nil, fmt.Errorf("codex reviewer transport unavailable: materialize probe workspace: %w", err)
		}
	}
	commandContext := adapter.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	binary, environment, err := codexReviewerRuntime(binary, scratch)
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: prepare private runtime: %w", err)
	}
	arguments, err := codexReviewerArguments(binary, workdir, probe != nil)
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: %w", err)
	}
	if model.ValidCodexReviewModel(adapter.Model) {
		arguments = append(arguments, "--model", adapter.Model)
	}
	command := commandContext(ctx, binary, arguments...)
	command.Env = environment
	command.Dir = workdir
	command.Stdin = bytes.NewReader(invocation.Prompt())
	var stderr, stdout bytes.Buffer
	command.Stderr = &stderr
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("codex reviewer transport failed: %w: %s", err, stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("codex reviewer transport produced no final message")
	}
	return stdout.Bytes(), nil
}

// codexReviewerArguments selects a deny-by-default native profile. Only the
// probing refuter's candidate copy receives host write access. Raw final stdout
// avoids adding a result-file writable root or parsing model events here.
func codexReviewerArguments(binary, workdir string, probe bool) ([]string, error) {
	arguments := []string{
		"--no-daemon", "--ask-for-approval", "never", "exec", "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--strict-config",
		"-C", workdir, "--color", "never",
		"--config", `model_provider="openai"`, "--config", `forced_login_method="chatgpt"`,
		"--config", "features.memories=false", "--config", "memories.use_memories=false", "--config", "memories.generate_memories=false",
		"--config", `history.persistence="none"`, "--config", "project_doc_max_bytes=0", "--config", `project_root_markers=["."]`,
		"--config", `shell_environment_policy.inherit="none"`, "--config", "features.shell_snapshot=false",
		"--config", "features.plugins=false", "--config", "features.apps=false", "--config", "features.hooks=false", "--config", "features.multi_agent=false",
		"--config", `web_search="disabled"`, "--config", "features.image_generation=false", "--config", "features.browser_use=false", "--config", "features.computer_use=false",
	}
	permissions, err := codexReviewerPermissions(binary, workdir, probe)
	if err != nil {
		return nil, err
	}
	arguments = append(arguments, permissions...)

	baseURL, enabled, err := codexReviewerLoopbackBaseURL(os.Getenv(codexReviewerLoopbackBaseURLEnvironment))
	if err != nil {
		return nil, err
	}
	if !enabled {
		return arguments, nil
	}

	return append(arguments,
		"--config", `model_provider="`+codexReviewerLoopbackProviderID+`"`,
		"--config", fmt.Sprintf(`model_providers.%s={name="Gentle AI reviewer loopback",base_url=%q,wire_api="responses"}`, codexReviewerLoopbackProviderID, baseURL),
	), nil
}

func codexReviewerLoopbackBaseURL(raw string) (string, bool, error) {
	if raw == "" {
		return "", false, nil
	}
	if strings.TrimSpace(raw) != raw {
		return "", false, fmt.Errorf("loopback base URL must not contain leading or trailing whitespace")
	}

	endpoint, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid loopback base URL: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", false, fmt.Errorf("loopback base URL scheme must be http or https")
	}
	if endpoint.User != nil {
		return "", false, fmt.Errorf("loopback base URL must not include credentials")
	}
	if endpoint.Host == "" || endpoint.Opaque != "" {
		return "", false, fmt.Errorf("loopback base URL must include a host")
	}
	host := net.ParseIP(endpoint.Hostname())
	if host == nil || !host.IsLoopback() {
		return "", false, fmt.Errorf("loopback base URL host must be a numeric loopback address")
	}
	port := endpoint.Port()
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return "", false, fmt.Errorf("loopback base URL must include a valid port")
	}
	if endpoint.Path != "/v1" || endpoint.RawPath != "" {
		return "", false, fmt.Errorf("loopback base URL path must be exactly /v1")
	}
	if endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return "", false, fmt.Errorf("loopback base URL must not include a query or fragment")
	}

	return endpoint.String(), true, nil
}
