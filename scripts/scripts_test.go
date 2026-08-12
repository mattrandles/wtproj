package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestE2ESmokeScriptSucceedsInIsolatedFixture(t *testing.T) {
	requireUnixShell(t)
	result := runScript(t, "scripts/e2e_smoke.sh")
	if result.err != nil {
		t.Fatalf("e2e_smoke.sh: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.output, "smoke test passed") {
		t.Fatalf("e2e_smoke.sh output = %q, want success marker", result.output)
	}
}

func TestAllocationIndexAssertionPassesFreshUnbornNamedBranch(t *testing.T) {
	requireCommand(t, "git")
	requireCommand(t, "go")
	repoRoot := repositoryRoot(t)
	project := filepath.Join(t.TempDir(), "fresh unborn project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("create project: %v", err)
	}
	runCommand(t, project, "git", "init", "-q")
	runCommand(t, project, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	if command := exec.Command("git", "-C", project, "rev-parse", "--verify", "HEAD"); command.Run() == nil {
		t.Fatal("fresh repository unexpectedly has a commit")
	}

	binaryName := "wtp"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	runCommand(t, repoRoot, "go", "build", "-o", binaryPath, "./cmd/wtp")
	createdOutput := runCommand(t, project, binaryPath, "--json", "task", "create", "--title", "fresh unborn regression")
	var created struct {
		ShortID string `json:"shortId"`
	}
	if err := json.Unmarshal(createdOutput, &created); err != nil {
		t.Fatalf("decode task create output: %v\n%s", err, createdOutput)
	}
	if created.ShortID == "" {
		t.Fatal("task create returned an empty shortId")
	}
	runCommand(t, project, binaryPath, "--json", "task", "next", "--agent", "allocation-regression")
	output := runCommand(t, repoRoot, "go", "run", filepath.Join(repoRoot, "scripts/assert_allocation_index.go"), "--project-dir", project, "--store-dir", ".wtp", "--task-id", created.ShortID)
	if !strings.Contains(string(output), "allocation index assertion passed") {
		t.Fatalf("allocation assertion output = %q, want success marker", output)
	}
}

func TestInstallLocalScriptSucceedsWithoutTouchingRepository(t *testing.T) {
	requireUnixShell(t)
	installDir := t.TempDir()
	result := runScriptWithEnv(t, "scripts/install_local.sh", []string{
		"WTP_INSTALL_DIR=" + installDir,
		"WTP_INSTALL_NAME=wtp-test",
	})
	if result.err != nil {
		t.Fatalf("install_local.sh: %v\n%s", result.err, result.output)
	}
	installed := filepath.Join(installDir, "wtp-test")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("installed executable: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("installed executable mode = %o, want executable", info.Mode())
	}
	version := exec.Command(installed, "version")
	if output, err := version.CombinedOutput(); err != nil {
		t.Fatalf("installed version: %v\n%s", err, output)
	}
}

func TestInstallLocalScriptReportsFailureWithoutInstalling(t *testing.T) {
	requireUnixShell(t)
	installPath := filepath.Join(t.TempDir(), "install target")
	if err := os.WriteFile(installPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("create invalid install directory: %v", err)
	}

	result := runScriptWithEnv(t, "scripts/install_local.sh", []string{
		"WTP_INSTALL_DIR=" + installPath,
		"WTP_INSTALL_NAME=wtp-test",
	})
	if result.err == nil {
		t.Fatal("install_local.sh unexpectedly succeeded with a file as install directory")
	}
	if _, err := os.Stat(filepath.Join(installPath, "wtp-test")); err == nil {
		t.Fatalf("failed install created target: %v", err)
	}
	if got, err := os.ReadFile(installPath); err != nil || string(got) != "existing" {
		t.Fatalf("failed install changed existing path: contents=%q error=%v", got, err)
	}
}

func TestVerifyScriptRejectsUnknownMode(t *testing.T) {
	requireUnixShell(t)
	result := runScript(t, "scripts/verify.sh", "unsupported")
	if result.err == nil {
		t.Fatal("verify.sh unexpectedly accepted an unknown mode")
	}
	if !strings.Contains(result.output, "unknown mode 'unsupported'") {
		t.Fatalf("verify.sh output = %q, want unknown-mode error", result.output)
	}
}

type scriptResult struct {
	output string
	err    error
}

func runScript(t *testing.T, relativePath string, args ...string) scriptResult {
	t.Helper()
	return runScriptWithEnv(t, relativePath, nil, args...)
}

func runScriptWithEnv(t *testing.T, relativePath string, extraEnv []string, args ...string) scriptResult {
	t.Helper()
	repoRoot := repositoryRoot(t)
	commandArgs := append([]string{filepath.Join(repoRoot, relativePath)}, args...)
	command := exec.Command("bash", commandArgs...)
	command.Dir = repoRoot
	if len(extraEnv) > 0 {
		command.Env = append(os.Environ(), extraEnv...)
	}
	output, err := command.CombinedOutput()
	return scriptResult{output: string(output), err: err}
}

func runCommand(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return output
}

func requireCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}

func requireUnixShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Bash scripts are not run on Windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate script test")
	}
	return filepath.Dir(filepath.Dir(filename))
}
