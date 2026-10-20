package reviewerprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCodexReviewerPrivateRuntime(t *testing.T) {
	source := t.TempDir()
	auth := filepath.Join(source, "auth.json")
	if err := os.WriteFile(auth, []byte("synthetic file; no credentials"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", source)
	t.Setenv("OPENAI_API_KEY", "must-not-inherit")
	t.Setenv("CODEX_THREAD_ID", "must-not-inherit")
	t.Setenv("SSH_AUTH_SOCK", "must-not-inherit")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, env, err := codexReviewerRuntime(binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range env {
		if strings.Contains(entry, "must-not-inherit") {
			t.Fatal("ambient context or credential leaked")
		}
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "CODEX_HOME=") {
			home := strings.TrimPrefix(entry, "CODEX_HOME=")
			target, err := os.Readlink(filepath.Join(home, "auth.json"))
			if err != nil || target != auth {
				t.Fatalf("login was not linked: %v", err)
			}
			if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatal("runtime home is not private")
			}
		}
	}
}

func TestCodexReviewerDisablesProjectInstructions(t *testing.T) {
	work := t.TempDir()
	skill := filepath.Join(work, ".agents", "skills", "private", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("untrusted instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	args, err := codexReviewerPermissions("/usr/bin/codex", work, false)
	if err != nil || !slices.Contains(args, "skills.config=[{path="+strconv.Quote(skill)+",enabled=false}]") {
		t.Fatalf("project skill not disabled: %q %v", args, err)
	}
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/outside/SKILL.md", skill); err != nil {
		t.Fatal(err)
	}
	if _, err := codexReviewerPermissions("/usr/bin/codex", work, false); err == nil {
		t.Fatal("instruction-discovery symlink followed")
	}
	if err := os.RemoveAll(filepath.Join(work, ".agents")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(work, ".agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := codexReviewerPermissions("/usr/bin/codex", work, false); err == nil {
		t.Fatal("instruction parent symlink followed")
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "permissions.gentle_reviewer.filesystem=") && (!strings.Contains(arg, `"/usr/bin/codex"="read"`) || strings.Contains(arg, `"/usr/bin"="read"`)) {
			t.Fatal("runtime grant extends beyond executable")
		}
	}

}

// Opt-in host test: real Codex exec and real sandbox, synthetic local model.
// Never run this inside a Codex sandbox or confuse it with a live model review.
func TestCodexAdapterHostIsolation(t *testing.T) {
	if os.Getenv("GENTLE_AI_CODEX_HOST_PROBE") != "1" {
		t.Skip("requires an explicit host-only probe")
	}
	if os.Getenv("CODEX_SANDBOX_NETWORK_DISABLED") != "" {
		t.Fatal("host test must not run inside a Codex sandbox")
	}
	for _, probe := range []bool{false, true} {
		t.Run(fmt.Sprintf("probe=%t", probe), func(t *testing.T) {
			outside := t.TempDir()
			protected := filepath.Join(outside, "protected")
			if err := os.WriteFile(protected, []byte("synthetic canary"), 0o600); err != nil {
				t.Fatal(err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			sourceHome := t.TempDir()
			original := os.Getenv("CODEX_HOME")
			if original == "" {
				original = filepath.Join(home, ".codex")
			}
			if err := os.Symlink(filepath.Join(original, "auth.json"), filepath.Join(sourceHome, "auth.json")); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"AGENTS.md", "config.toml", "memories/memory_summary.md", "skills/private/SKILL.md"} {
				path := filepath.Join(sourceHome, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("UNINTENDED_USER_CONTEXT_CANARY"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CODEX_HOME", sourceHome)
			var actualWork string
			virtualOnly := false
			candidateHostWrite := false
			calls := 0
			captured := make(chan []byte, 1)
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Input json.RawMessage `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "invalid", 400)
					return
				}
				calls++
				if calls == 1 && strings.Contains(string(request.Input), "UNINTENDED_") {
					t.Error("inherited or project instructions entered provider input")
				}
				item := map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "controlled probe complete", "annotations": []any{}}}}
				if calls == 1 {
					code := fmt.Sprintf(`import os,json,socket
from pathlib import Path
x={}
for k,p,m in [('candidate_read',Path('candidate.txt'),'r'),('protected_read',Path(%q),'r'),('auth_read',Path(%q),'r'),('ssh_read',Path(%q),'r'),('gh_read',Path(%q),'r'),('candidate_write',Path('created'),'w'),('outside_write',Path(%q),'w'),('namespace_write',Path.cwd().parent/'unbacked-file','w'),('runtime_auth_read',Path.cwd().parent/'runtime'/'auth.json','r')]:
 try:
  fd=os.open(p,os.O_RDONLY if m=='r' else os.O_WRONLY|os.O_CREAT,0o600)
  if m=='w':os.write(fd,b'probe')
  os.close(fd);x[k]=True
 except OSError:x[k]=False
try:
 s=socket.create_connection(('127.0.0.1',%s),timeout=1);s.close();x['host_network']=True
except OSError:x['host_network']=False
x['ambient_secret']='GENTLE_AMBIENT_SECRET' in os.environ
print(json.dumps(x))`, protected, filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "gh"), filepath.Join(outside, "created"), strings.Split(server.URL, ":")[2])
					cmd := "/usr/bin/python3 -c '" + strings.ReplaceAll(code, "'", "'\"'\"'") + "'"
					input, _ := json.Marshal(map[string]any{"cmd": cmd, "max_output_tokens": 1500})
					escalation, _ := json.Marshal(map[string]any{"cmd": "/usr/bin/true", "sandbox_permissions": "require_escalated", "justification": "controlled negative probe; approval must remain never"})
					item = map[string]any{"id": "tool_probe", "type": "custom_tool_call", "call_id": "call_probe", "name": "exec", "input": "text(await tools.exec_command(" + string(input) + ")); text(await tools.exec_command(" + string(escalation) + "))"}
				} else {
					_, err := os.Stat(filepath.Join(filepath.Dir(actualWork), "unbacked-file"))
					virtualOnly = os.IsNotExist(err)
					_, err = os.Stat(filepath.Join(actualWork, "created"))
					candidateHostWrite = err == nil
					captured <- request.Input
				}
				w.Header().Set("Content-Type", "text/event-stream")
				created, _ := json.Marshal(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_probe", "status": "in_progress"}})
				fmt.Fprintf(w, "event: response.created\ndata: %s\n\n", created)
				output := []any{item}
				index := 0
				if calls == 1 {
					commentary := map[string]any{"id": "commentary_probe", "type": "message", "role": "assistant", "phase": "commentary", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "INTERIM_TRANSPORT_MARKER", "annotations": []any{}}}}
					output = []any{commentary, item}
					index = 1
					for _, name := range []string{"response.output_item.added", "response.output_item.done"} {
						b, _ := json.Marshal(map[string]any{"type": name, "output_index": 0, "item": commentary})
						fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
					}
				}
				for _, event := range []struct {
					name string
					data map[string]any
				}{
					{"response.output_item.added", map[string]any{"output_index": index, "item": item}},
					{"response.output_item.done", map[string]any{"output_index": index, "item": item}},
					{"response.completed", map[string]any{"response": map[string]any{"id": "resp_probe", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}},
				} {
					event.data["type"] = event.name
					b, _ := json.Marshal(event.data)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, b)
				}
			}))
			defer server.Close()
			t.Setenv(codexReviewerLoopbackBaseURLEnvironment, server.URL+"/v1")
			t.Setenv("GENTLE_AMBIENT_SECRET", "synthetic secret")
			adapter := NewCodexAdapter()
			var arguments []string
			adapter.commandContext = func(ctx context.Context, binary string, args ...string) *exec.Cmd {
				arguments = append([]string(nil), args...)
				work := codexTestArgument(t, args, "-C")
				actualWork = work
				// Fixture injection only; production argv, Env and process are unchanged.
				if err := os.WriteFile(filepath.Join(work, "candidate.txt"), []byte("frozen fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				return exec.CommandContext(ctx, binary, args...)
			}
			invocation := NewInvocation([]byte("Controlled offline isolation probe; no real model."))
			if probe {
				invocation = invocation.WithProbeWorkspace(outside, func(_ context.Context, work string) error {
					for _, name := range []string{"AGENTS.md", ".codex/config.toml", ".agents/skills/private/SKILL.md"} {
						path := filepath.Join(work, name)
						if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
							return err
						}
						text := "UNINTENDED_PROJECT_CONTEXT_CANARY"
						if strings.HasSuffix(name, "config.toml") {
							text = `developer_instructions="UNINTENDED_PROJECT_CONFIG_CANARY"`
						}
						if strings.HasSuffix(name, "SKILL.md") {
							text = "---\nname: UNINTENDED_PROJECT_SKILL_CANARY\ndescription: project-only private canary\n---\nnever inject"
						}
						if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
							return err
						}
					}
					return nil
				})
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			raw, err := adapter.Review(ctx, invocation)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(raw)) != "controlled probe complete" {
				t.Fatalf("unexpected raw output %q", raw)
			}
			assertCodexIsolation(t, arguments, probe)
			var input []struct {
				Type   string `json:"type"`
				Output []struct {
					Text string `json:"text"`
				} `json:"output"`
			}
			select {
			case b := <-captured:
				if err := json.Unmarshal(b, &input); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatal("sandbox tool was not called")
			}
			var observed map[string]bool
			escalationDenied := false
			for _, item := range input {
				if item.Type == "custom_tool_call_output" {
					for _, part := range item.Output {
						lower := strings.ToLower(part.Text)
						if strings.Contains(lower, "approval policy") && strings.Contains(lower, "never") {
							escalationDenied = true
						}
						var result struct {
							Output string `json:"output"`
						}
						if json.Unmarshal([]byte(part.Text), &result) == nil && result.Output != "" {
							_ = json.Unmarshal([]byte(result.Output), &observed)
						}
					}
				}
			}
			if len(observed) != 11 || !observed["candidate_read"] || observed["candidate_write"] != probe {
				t.Fatalf("missing or wrong sandbox results: %v", observed)
			}
			for k, v := range observed {
				if k != "candidate_read" && k != "candidate_write" && k != "namespace_write" && v {
					t.Errorf("isolation bypass: %s", k)
				}
			}
			if !escalationDenied {
				t.Error("escalation request was not explicitly refused by never policy")
			}
			if candidateHostWrite != probe {
				t.Error("candidate host-write permission differs from selected mode")
			}
			if !virtualOnly {
				t.Error("unbacked namespace write affected the host")
			}
			t.Logf("actual host exec restrictions: %v; namespace writes are virtual-only=%t", observed, virtualOnly)
		})
	}
}

func TestCodexAdapterHostAuthentication(t *testing.T) {
	if os.Getenv("GENTLE_AI_CODEX_HOST_PROBE") != "1" {
		t.Skip("requires host-only probe")
	}
	if os.Getenv("CODEX_SANDBOX_NETWORK_DISABLED") != "" {
		t.Fatal("host-only")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	binary, env, err := codexReviewerRuntime(binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), binary, "--no-daemon", "login", "status")
	command.Env = env
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Logged in using ChatGPT") {
		t.Fatalf("subscription authentication unavailable: %v", err)
	}
	t.Log("Trusted client recognizes existing ChatGPT login through the private-home auth link; no model request")
}
