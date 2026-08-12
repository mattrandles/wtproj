//go:build ignore

// assert_allocation_index validates the allocation index produced by a
// workflow that created one task in a disposable project. It is kept as a
// standalone go run target so Bash and PowerShell use the same contract.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/runtimecontext"
)

type allocationIndex struct {
	Branch string `json:"branch"`
	Next   int    `json:"next"`
}

type taskRecord struct {
	ID        string `json:"id"`
	ShortID   string `json:"shortId"`
	GitBranch string `json:"gitBranch"`
}

func main() {
	projectDir := flag.String("project-dir", ".", "project directory whose Git context should be checked")
	storeDir := flag.String("store-dir", ".wtp", "task store directory, relative to project-dir unless absolute")
	taskID := flag.String("task-id", "", "short ID returned for the workflow-created task")
	flag.Parse()
	if *taskID == "" {
		fail("--task-id is required")
	}

	project, err := filepath.Abs(*projectDir)
	if err != nil {
		fail("resolve project directory: %v", err)
	}
	store := *storeDir
	if !filepath.IsAbs(store) {
		store = filepath.Join(project, store)
	}

	context, err := runtimecontext.Discover(project)
	if err != nil {
		fail("discover project context: %v", err)
	}
	parts, err := core.ParseShortID(*taskID)
	if err != nil {
		fail("parse workflow task ID: %v", err)
	}

	metaDir := filepath.Join(store, "meta")
	entries, err := os.ReadDir(metaDir)
	if err != nil {
		fail("read allocation metadata: %v", err)
	}
	var indexNames []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && (entry.Name() == "index.json" || (strings.HasPrefix(entry.Name(), "index-") && strings.HasSuffix(entry.Name(), ".json"))) {
			indexNames = append(indexNames, entry.Name())
		}
	}
	slices.Sort(indexNames)

	scope := context.Scope()
	expectedIndex := "index.json"
	if scope != nil {
		expectedIndex = "index-" + scope.BranchID + ".json"
		if !parts.IsScoped() || parts.BranchID != scope.BranchID {
			fail("workflow task %q is not scoped to branch %q (%s)", *taskID, scope.Branch, scope.BranchID)
		}
	} else if !parts.IsLegacy() {
		fail("workflow task %q is scoped, but the invocation has no named branch", *taskID)
	}

	if !slices.Equal(indexNames, []string{expectedIndex}) {
		fail("allocation indexes = %v, want exactly [%s]", indexNames, expectedIndex)
	}
	index, err := readJSON[allocationIndex](filepath.Join(metaDir, expectedIndex))
	if err != nil {
		fail("decode %s: %v", expectedIndex, err)
	}
	if scope != nil {
		if index.Branch != scope.Branch {
			fail("%s branch = %q, want %q", expectedIndex, index.Branch, scope.Branch)
		}
	} else if index.Branch != "" {
		fail("legacy index branch = %q, want empty", index.Branch)
	}
	if index.Next < 1 {
		fail("%s next = %d, want a positive allocation", expectedIndex, index.Next)
	}

	task, err := findTask(store, *taskID)
	if err != nil {
		fail("read workflow task %q: %v", *taskID, err)
	}
	if task.ID == "" || task.ShortID != *taskID {
		fail("workflow task record = id %q shortId %q, want a valid record for %q", task.ID, task.ShortID, *taskID)
	}
	if scope != nil {
		if task.GitBranch != scope.Branch {
			fail("workflow task gitBranch = %q, want %q", task.GitBranch, scope.Branch)
		}
	} else if task.GitBranch != "" {
		fail("legacy workflow task gitBranch = %q, want empty", task.GitBranch)
	}
	sequence, err := strconv.Atoi(parts.Sequence)
	if err != nil {
		fail("parse workflow task sequence %q: %v", parts.Sequence, err)
	}
	if sequence == int(^uint(0)>>1) || index.Next != sequence+1 {
		fail("%s next = %d, want next allocation %d after task %s", expectedIndex, index.Next, sequence+1, *taskID)
	}

	fmt.Printf("allocation index assertion passed: %s branch=%q next=%d task=%s\n", expectedIndex, index.Branch, index.Next, *taskID)
}

func findTask(store, shortID string) (taskRecord, error) {
	var found taskRecord
	var foundPath string
	for _, status := range []string{"todo", "inProgress", "paused", "done"} {
		path := filepath.Join(store, status, shortID+".json")
		task, err := readJSON[taskRecord](path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return taskRecord{}, fmt.Errorf("decode %s: %w", path, err)
		}
		if foundPath != "" {
			return taskRecord{}, fmt.Errorf("found in both %s and %s", foundPath, path)
		}
		found, foundPath = task, path
	}
	if foundPath == "" {
		return taskRecord{}, os.ErrNotExist
	}
	return found, nil
}

func readJSON[T any](path string) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, errors.New("contains multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "allocation index assertion failed: "+format+"\n", args...)
	os.Exit(1)
}
