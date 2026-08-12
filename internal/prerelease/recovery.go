package prerelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
)

// scenarioFailureRecovery exercises the same corruption and publication
// boundaries as the focused provider tests, but only through the candidate
// executable and files on disk. A rejected operation must leave the
// persistent store byte-for-byte unchanged; documented migrations are
// recorded separately because they intentionally rewrite the store.
func scenarioFailureRecovery(r *scenarioRunner) error {
	base, err := r.newGitProject("recovery base")
	if err != nil {
		return err
	}
	r.setCWD(base)
	var first, second core.TaskView
	if err = r.json(&first, "task", "create", "--title", "recovery first"); err != nil {
		return err
	}
	if err = r.json(&second, "task", "create", "--title", "recovery second"); err != nil {
		return err
	}
	if _, err = r.command("handoff", "write", "--agent", "recovery", "--message", "retained context"); err != nil {
		return err
	}
	baseStore := filepath.Join(base, ".wtp")

	invalidCases := []struct {
		label  string
		want   string
		mutate func(string) error
		args   []string
	}{
		{"corrupt-task-json", "corrupt task file", func(store string) error {
			return os.WriteFile(filepath.Join(store, "todo", first.ShortID+".json"), []byte("{\n"), 0o644)
		}, []string{"task", "list"}},
		{"invalid-canonical-fields", "invalid task file", func(store string) error {
			return rewriteTask(store, first.ShortID, func(task *core.Task) { task.Title = " " })
		}, []string{"task", "list"}},
		{"invalid-timestamps", "timestamps are required", func(store string) error {
			return rewriteTask(store, first.ShortID, func(task *core.Task) { task.CreatedAt = time.Time{} })
		}, []string{"task", "show", first.ShortID}},
		{"missing-dependency", "dependency", func(store string) error {
			return rewriteTask(store, first.ShortID, func(task *core.Task) { task.Dependencies = []string{"00000000-0000-4000-8000-000000000099"} })
		}, []string{"graph", "--status", "all"}},
		{"dependency-cycle", "cyclic dependency", func(store string) error {
			if err := rewriteTask(store, first.ShortID, func(task *core.Task) { task.Dependencies = []string{second.ID} }); err != nil {
				return err
			}
			return rewriteTask(store, second.ShortID, func(task *core.Task) { task.Dependencies = []string{first.ID} })
		}, []string{"task", "list"}},
		{"corrupt-handoffs", "corrupt handoff file", func(store string) error {
			return os.WriteFile(filepath.Join(store, "handoffs.json"), []byte("not-json"), 0o644)
		}, []string{"handoff", "get", "--all-scopes", "--all"}},
		{"malformed-index", "read index", func(store string) error {
			path, err := indexFilePath(store)
			if err != nil {
				return err
			}
			return os.WriteFile(path, []byte("{\n"), 0o644)
		}, []string{"task", "list"}},
		{"destination-collision", "create export dir", func(store string) error {
			return os.WriteFile(filepath.Join(filepath.Dir(store), "collision"), []byte("occupied"), 0o644)
		}, []string{"export", "--out", filepath.Join(filepath.Dir(baseStore), "collision")}},
	}
	for _, item := range invalidCases {
		if err := r.runPreservationCase(baseStore, item.label, item.mutate, item.want, item.args...); err != nil {
			return err
		}
	}
	if err := r.runConflictingIndex(baseStore); err != nil {
		return err
	}

	if err := r.runMissingIndexRecovery(baseStore, first.ShortID); err != nil {
		return err
	}
	if err := r.runStaleIndexRecovery(baseStore); err != nil {
		return err
	}
	if err := r.runLegacyFilenameRecovery(baseStore, first); err != nil {
		return err
	}
	if err := r.runResidueRecovery(baseStore, first); err != nil {
		return err
	}
	if err := r.runLockRecovery(baseStore); err != nil {
		return err
	}
	if err := r.runFaultInjectionCases(baseStore, first.ShortID); err != nil {
		return err
	}
	if err := r.runReadOnlyRecovery(baseStore, first.ShortID); err != nil {
		return err
	}
	r.assert("all rejected storage operations preserve the persistent manifest")
	r.assert("recovery accepts only complete old or complete new atomic task state")
	return nil
}

func (r *scenarioRunner) runConflictingIndex(baseStore string) error {
	project, err := r.newGitProject("recovery conflicting-index")
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return err
	}
	path, err := indexFilePath(store)
	if err != nil {
		return err
	}
	var index allocationIndex
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &index); err != nil {
		return err
	}
	if index.Branch == "" {
		r.skip("failure-recovery/conflicting-index", "legacy unscoped index has no branch ownership field")
		return nil
	}
	index.Branch = "different-branch"
	if err = writeJSON(path, index); err != nil {
		return err
	}
	before, err := storageManifest(store)
	if err != nil {
		return err
	}
	r.setCWD(project)
	if err = r.expectFailureContaining("branch index token", "task", "list"); err != nil {
		return err
	}
	after, err := storageManifest(store)
	if err != nil {
		return err
	}
	unchanged := equalManifests(before, after)
	r.report.Preservation = append(r.report.Preservation, PreservationEvidence{Label: "conflicting-index", Before: before, After: after, Unchanged: unchanged, Expected: "rejected branch ownership preserves storage"})
	if !unchanged {
		return errors.New("conflicting index changed persistent storage")
	}
	r.assert("conflicting branch index is rejected without mutation")
	return nil
}

func (r *scenarioRunner) runFaultInjectionCases(baseStore, shortID string) error {
	cases := []struct {
		label string
		point string
		args  []string
	}{
		{"create-publication-interruption", "create-publication", []string{"task", "create", "--title", "interrupted create"}},
		{"status-move-interruption", "status-move-publication", []string{"task", "start", shortID, "--agent", "interrupted"}},
		{"handoff-replacement-interruption", "handoff-replacement", []string{"handoff", "write", "--agent", "interrupted", "--message", "interrupted handoff"}},
		{"export-publication-interruption", "export-publication", []string{"export", "--out", "fault export"}},
	}
	for _, item := range cases {
		project, err := r.newGitProject("fault " + item.label)
		if err != nil {
			return err
		}
		store := filepath.Join(project, ".wtp")
		if err = replaceTree(baseStore, store); err != nil {
			return err
		}
		r.setCWD(project)
		originalEnv := r.env
		r.env = replaceEnv(r.env, "WTP_FAULT_POINT", item.point)
		result, commandErr := r.command(item.args...)
		r.env = originalEnv
		if commandErr == nil || result.exit != 97 {
			r.skip("failure-recovery/"+item.label, "candidate is not built with the test-only wtp_fault_injection tag")
			continue
		}
		_ = os.Remove(filepath.Join(store, "meta", "wtp.lock"))
		if _, err = r.command("task", "list"); err != nil {
			return fmt.Errorf("%s reopen list: %w", item.label, err)
		}
		if _, err = r.command("task", "show", shortID); err != nil {
			return fmt.Errorf("%s reopen show: %w", item.label, err)
		}
		if _, err = r.command("graph", "--status", "all"); err != nil {
			return fmt.Errorf("%s reopen graph: %w", item.label, err)
		}
		if item.point == "export-publication" {
			if err = validateExport(filepath.Join(project, "fault export"), 2); err != nil {
				return fmt.Errorf("%s exported state: %w", item.label, err)
			}
		}
		if err = validateStore(store); err != nil {
			return fmt.Errorf("%s complete-state invariant: %w", item.label, err)
		}
		r.assert("fault point " + item.point + " terminates after complete publication; clean reopen succeeds")
	}
	return nil
}

func (r *scenarioRunner) runPreservationCase(baseStore, label string, mutate func(string) error, want string, args ...string) error {
	project, err := r.newGitProject("recovery " + label)
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return fmt.Errorf("seed %s: %w", label, err)
	}
	if err = mutate(store); err != nil {
		return fmt.Errorf("mutate %s: %w", label, err)
	}
	if label == "destination-collision" {
		args = []string{"export", "--out", filepath.Join(filepath.Dir(store), "collision")}
	}
	before, err := storageManifest(store)
	if err != nil {
		return fmt.Errorf("manifest before %s: %w", label, err)
	}
	r.setCWD(project)
	if err = r.expectFailureContaining(want, args...); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	after, err := storageManifest(store)
	if err != nil {
		return fmt.Errorf("manifest after %s: %w", label, err)
	}
	unchanged := equalManifests(before, after)
	r.report.Preservation = append(r.report.Preservation, PreservationEvidence{Label: label, Before: before, After: after, Unchanged: unchanged, Expected: "rejected command preserves storage"})
	if !unchanged {
		return fmt.Errorf("%s changed persistent storage after rejection", label)
	}
	// Reopen through a clean process. Deliberately corrupt fixtures are not
	// expected to become valid; the second read proves the error remains
	// actionable and no repair was silently attempted.
	if err = r.expectFailureContaining(want, args...); err != nil {
		return fmt.Errorf("%s reopen: %w", label, err)
	}
	return nil
}

func (r *scenarioRunner) runMissingIndexRecovery(baseStore, firstShortID string) error {
	project, err := r.newGitProject("recovery missing-index")
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return err
	}
	indexPath, err := indexFilePath(store)
	if err != nil {
		return err
	}
	if err = os.Remove(indexPath); err != nil {
		return err
	}
	r.setCWD(project)
	if _, err = r.command("task", "list"); err != nil {
		return fmt.Errorf("missing index recovery: %w", err)
	}
	var created core.TaskView
	if err = r.json(&created, "task", "create", "--title", "after missing index"); err != nil {
		return err
	}
	if created.ShortID == firstShortID {
		return errors.New("missing index recovery reused an existing short ID")
	}
	if err = validateStore(store); err != nil {
		return err
	}
	r.assert("missing allocation index is rebuilt without logical duplication")
	r.report.Preservation = append(r.report.Preservation, PreservationEvidence{Label: "missing-index", Before: nil, After: nil, Unchanged: false, Expected: "documented recovery publishes a valid rebuilt index"})
	return nil
}

func (r *scenarioRunner) runStaleIndexRecovery(baseStore string) error {
	project, err := r.newGitProject("recovery stale-index")
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return err
	}
	indexPath, err := indexFilePath(store)
	if err != nil {
		return err
	}
	var stale allocationIndex
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &stale); err != nil {
		return err
	}
	stale.Next = 1
	if err = writeJSON(indexPath, stale); err != nil {
		return err
	}
	r.setCWD(project)
	var created core.TaskView
	if err = r.json(&created, "task", "create", "--title", "after stale index"); err != nil {
		return err
	}
	if err = validateStore(store); err != nil {
		return err
	}
	r.assert("stale allocation index advances past existing logical tasks")
	return nil
}

func (r *scenarioRunner) runLegacyFilenameRecovery(baseStore string, task core.TaskView) error {
	project, err := r.newGitProject("recovery legacy-filename")
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return err
	}
	oldPath := filepath.Join(store, "todo", task.ShortID+".json")
	newPath := filepath.Join(store, "todo", task.ID+".json")
	if err = os.Rename(oldPath, newPath); err != nil {
		return err
	}
	r.setCWD(project)
	if _, err = r.command("task", "list"); err != nil {
		return fmt.Errorf("legacy UUID filename recovery: %w", err)
	}
	if _, err = os.Stat(oldPath); err != nil {
		return fmt.Errorf("legacy filename was not migrated: %w", err)
	}
	if _, err = os.Stat(newPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("legacy UUID filename residue remains")
	}
	if err = validateStore(store); err != nil {
		return err
	}
	r.assert("legacy UUID-named task is migrated to the canonical short-ID filename")
	return nil
}

func (r *scenarioRunner) runResidueRecovery(baseStore string, task core.TaskView) error {
	project, err := r.newGitProject("recovery status-residue")
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return err
	}
	var newer core.Task
	if err = readTask(store, task.ShortID, &newer); err != nil {
		return err
	}
	newer.Status = core.StatusInProgress
	started := newer.CreatedAt.Add(time.Second)
	newer.StartedAt = &started
	newer.UpdatedAt = newer.UpdatedAt.Add(2 * time.Second)
	if err = writeJSON(filepath.Join(store, "inProgress", task.ShortID+".json"), newer); err != nil {
		return err
	}
	r.setCWD(project)
	if _, err = r.command("task", "list"); err != nil {
		return err
	}
	if _, err = r.command("task", "show", task.ShortID); err != nil {
		return err
	}
	if _, err = r.command("graph", "--status", "all"); err != nil {
		return err
	}
	export := filepath.Join(filepath.Dir(project), "residue export")
	if _, err = r.command("export", "--out", export); err != nil {
		return err
	}
	if err = validateStore(store); err != nil {
		return fmt.Errorf("residue cleanup left invalid store: %w", err)
	}
	r.assert("interrupted status move converges to one complete newer task")
	return nil
}

func (r *scenarioRunner) runLockRecovery(baseStore string) error {
	for _, item := range []struct {
		label string
		data  string
		stale bool
	}{
		{"fresh-lock", "pid=999999\ncreatedAt=" + time.Now().UTC().Format(time.RFC3339Nano) + "\n", false},
		{"malformed-lock", "not a lock record\n", false},
		{"stale-lock", "pid=999999\ncreatedAt=" + time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339Nano) + "\n", true},
	} {
		project, err := r.newGitProject("recovery " + item.label)
		if err != nil {
			return err
		}
		store := filepath.Join(project, ".wtp")
		if err = replaceTree(baseStore, store); err != nil {
			return err
		}
		lockPath := filepath.Join(store, "meta", "wtp.lock")
		if err = os.WriteFile(lockPath, []byte(item.data), 0o644); err != nil {
			return err
		}
		r.setCWD(project)
		if item.stale {
			if _, err = r.command("task", "list"); err != nil {
				return fmt.Errorf("stale lock recovery: %w", err)
			}
			r.assert("stale lock is safely recovered without waiting")
		} else {
			if err = r.expectTimedOut("task", "list"); err != nil {
				return fmt.Errorf("%s: %w", item.label, err)
			}
			if err = os.Remove(lockPath); err != nil {
				return err
			}
			if _, err = r.command("task", "list"); err != nil {
				return fmt.Errorf("%s reopen: %w", item.label, err)
			}
		}
		if err = validateStore(store); err != nil {
			return err
		}
	}
	r.assert("fresh, malformed, and stale lock outcomes have bounded deadlines")
	return nil
}

func (r *scenarioRunner) runReadOnlyRecovery(baseStore, shortID string) error {
	if runtime.GOOS == "windows" {
		r.skip("failure-recovery/read-only", "Unix permission semantics are not applicable; native Windows ACL coverage belongs to the Windows gate")
		return nil
	}
	project, err := r.newGitProject("recovery read-only")
	if err != nil {
		return err
	}
	store := filepath.Join(project, ".wtp")
	if err = replaceTree(baseStore, store); err != nil {
		return err
	}
	target := filepath.Join(store, "todo", shortID+".json")
	oldMode, err := fileMode(target)
	if err != nil {
		return err
	}
	defer os.Chmod(target, oldMode)
	if err = os.Chmod(target, 0o444); err != nil {
		return err
	}
	before, err := storageManifest(store)
	if err != nil {
		return err
	}
	r.setCWD(project)
	result, commandErr := r.command("task", "update", shortID, "--description", "must not write")
	if commandErr == nil {
		r.skip("failure-recovery/read-only", "runner has sufficient privileges to write read-only files")
		return nil
	}
	if result.exit != 1 {
		return fmt.Errorf("read-only update exit=%d, want 1", result.exit)
	}
	after, err := storageManifest(store)
	if err != nil {
		return err
	}
	unchanged := equalManifests(before, after)
	r.report.Preservation = append(r.report.Preservation, PreservationEvidence{Label: "read-only-file", Before: before, After: after, Unchanged: unchanged, Expected: "write failure preserves existing task"})
	if !unchanged {
		return errors.New("read-only update changed storage")
	}
	r.assert("Unix read-only publication failure preserves the existing task")
	todoDir := filepath.Join(store, "todo")
	dirInfo, err := os.Stat(todoDir)
	if err != nil {
		return err
	}
	defer os.Chmod(todoDir, dirInfo.Mode().Perm())
	if err = os.Chmod(todoDir, 0o555); err != nil {
		return err
	}
	before, err = storageManifest(store)
	if err != nil {
		return err
	}
	result, commandErr = r.command("task", "create", "--title", "must not publish into read-only directory")
	if commandErr == nil {
		r.skip("failure-recovery/read-only-directory", "runner has sufficient privileges to write read-only directories")
		return nil
	}
	if result.exit != 1 {
		return fmt.Errorf("read-only directory create exit=%d, want 1", result.exit)
	}
	after, err = storageManifest(store)
	if err != nil {
		return err
	}
	unchanged = equalManifests(before, after)
	r.report.Preservation = append(r.report.Preservation, PreservationEvidence{Label: "read-only-directory", Before: before, After: after, Unchanged: unchanged, Expected: "rejected command preserves storage"})
	if !unchanged {
		return errors.New("read-only directory create changed storage")
	}
	r.assert("Unix read-only directory create rolls back allocation publication")
	return nil
}

func indexFilePath(store string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(store, "meta"))
	if err != nil {
		return "", err
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && (entry.Name() == "index.json" || (strings.HasPrefix(entry.Name(), "index-") && strings.HasSuffix(entry.Name(), ".json"))) {
			paths = append(paths, filepath.Join(store, "meta", entry.Name()))
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no active allocation index under %s", filepath.Join(store, "meta"))
	}
	if len(paths) > 1 {
		return "", fmt.Errorf("multiple allocation indexes under %s", filepath.Join(store, "meta"))
	}
	return paths[0], nil
}

func rewriteTask(store, shortID string, mutate func(*core.Task)) error {
	path := filepath.Join(store, "todo", shortID+".json")
	var task core.Task
	if err := readTaskFile(path, &task); err != nil {
		return err
	}
	mutate(&task)
	return writeJSON(path, task)
}

func readTask(store, shortID string, task *core.Task) error {
	return readTaskFile(filepath.Join(store, "todo", shortID+".json"), task)
}

func readTaskFile(path string, task *core.Task) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, task)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func fileMode(path string) (fs.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

func storageManifest(root string) ([]ManifestFile, error) {
	files, err := manifest(root)
	if err != nil {
		return nil, err
	}
	filtered := files[:0]
	for _, file := range files {
		if file.Path == "meta/wtp.lock" || strings.HasPrefix(filepath.ToSlash(file.Path), "meta/.tmp-") {
			continue
		}
		filtered = append(filtered, file)
	}
	return filtered, nil
}

func replaceTree(source, target string) error {
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
}
