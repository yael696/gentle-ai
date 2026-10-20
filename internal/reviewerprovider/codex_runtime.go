package reviewerprovider

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The trusted client retains file-backed login through a link, never a token
// copy. Its private state and the linked login stay outside the tool allowlist.
func codexReviewerRuntime(binary, scratch string) (string, []string, error) {
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", nil, err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	authHome := os.Getenv("CODEX_HOME")
	if authHome == "" {
		authHome = filepath.Join(home, ".codex")
	}
	authHome, err = filepath.Abs(authHome)
	if err != nil {
		return "", nil, err
	}
	runtimeHome := filepath.Join(scratch, "runtime")
	for _, dir := range []string{runtimeHome, filepath.Join(scratch, "home"), filepath.Join(scratch, "state"), filepath.Join(scratch, "xdg")} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", nil, err
		}
	}
	auth := filepath.Join(authHome, "auth.json")
	if _, err := os.Stat(auth); err == nil {
		if err := os.Symlink(auth, filepath.Join(runtimeHome, "auth.json")); err != nil {
			return "", nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	env := []string{
		"PATH=/usr/bin:/bin", "HOME=" + filepath.Join(scratch, "home"),
		"CODEX_HOME=" + runtimeHome, "CODEX_SQLITE_HOME=" + filepath.Join(scratch, "state"),
		"XDG_RUNTIME_DIR=" + filepath.Join(scratch, "xdg"),
		"XDG_CONFIG_HOME=" + filepath.Join(scratch, "home"), "XDG_DATA_HOME=" + filepath.Join(scratch, "state"),
	}
	return binary, env, nil
}

func codexReviewerPermissions(binary, workdir string, probe bool) ([]string, error) {
	access := "read"
	if probe {
		access = "write"
	}
	profile := fmt.Sprintf(`permissions.gentle_reviewer.filesystem={":minimal"="read",%s="read",%s=%s}`,
		strconv.Quote(binary), strconv.Quote(workdir), strconv.Quote(access))
	// Disable project skills as instructions, retaining their bytes as review
	// data. Never follow an instruction-discovery symlink out of the candidate.
	var skills []string
	for _, directory := range []string{".agents", ".codex"} {
		if info, err := os.Lstat(filepath.Join(workdir, directory)); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Codex reviewer instruction directory symlinks are unsupported")
		}
	}
	for _, dir := range []string{filepath.Join(workdir, ".agents", "skills"), filepath.Join(workdir, ".codex", "skills")} {
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("Codex reviewer project skill symlinks are unsupported")
			}
			if entry.Name() == "SKILL.md" {
				skills = append(skills, fmt.Sprintf("{path=%s,enabled=false}", strconv.Quote(path)))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return []string{"--config", `default_permissions="gentle_reviewer"`, "--config", profile,
		"--config", "permissions.gentle_reviewer.network.enabled=false",
		"--config", "projects." + strconv.Quote(workdir) + `.trust_level="untrusted"`,
		"--config", "skills.config=[" + strings.Join(skills, ",") + "]"}, nil
}
