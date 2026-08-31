package releaseasset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	assetDocument := readDocumentationFile(t, filepath.Join(root, "docs", "release-assets.md"))
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	documentation := strings.Join(strings.Fields(assetDocument+"\n"+readme), " ")

	for _, platform := range Platforms() {
		want := fmt.Sprintf("| `%s` | `%s` | `%s` |", platform.GOOS, platform.GOARCH, platform.AssetName)
		if !strings.Contains(assetDocument, want) {
			t.Errorf("release asset documentation missing contract row %q", want)
		}
	}

	for _, text := range []string{
		LatestReleaseURL,
		ChecksumAssetName,
		ChecksumAlgorithm,
		"https://github.com/mattrandles/wtproj/releases",
		"sole supported distribution channel",
		"latest/download",
		"wtp update",
	} {
		if !strings.Contains(documentation, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("release documentation missing %q", text)
		}
	}

	for _, text := range []string{
		"GitHub Release asset contract",
		"direct-download release QA guide",
		"implemented, supported backend is local flat-file storage",
	} {
		if !strings.Contains(readme, text) {
			t.Errorf("README missing %q", text)
		}
	}

	if strings.Contains(strings.ToLower(readme), "trello") {
		t.Error("README must not document the unimplemented Trello provider")
	}
}

func TestRetainedHandoffDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	contributing := readDocumentationFile(t, filepath.Join(root, "CONTRIBUTING.md"))
	changelog := readDocumentationFile(t, filepath.Join(root, "CHANGELOG.md"))

	for _, text := range []string{
		"### Retained handoffs",
		"wtp handoff write --message",
		"wtp handoff get [--task <task-id> | --all-scopes] [--limit N | --all]",
		"wtp handoff purge (--id <handoff-id> | --global | --task <task-id> | --all-scopes)",
		"Writes append by default",
		"Handoff reads and claim attachment are non-consuming",
		".wtp/handoffs.json",
		"missing file is compatible",
		"exact retained collection",
		"--export-tasks=<directory>",
	} {
		if !strings.Contains(readme, text) {
			t.Errorf("README missing retained handoff contract %q", text)
		}
	}

	for _, text := range []string{
		"### Retained handoff context",
		"Handoff reads and claim attachment are non-consuming",
		"Purge uses exactly one of",
		"`--export-tasks=<directory>` form remains an export alias",
	} {
		if !strings.Contains(contributing, text) {
			t.Errorf("CONTRIBUTING missing retained handoff guidance %q", text)
		}
	}

	if !strings.Contains(changelog, "Documented the retained handoff workflow") {
		t.Error("CHANGELOG missing retained handoff entry")
	}
}

func TestGroupingDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	changelog := readDocumentationFile(t, filepath.Join(root, "CHANGELOG.md"))
	taskManagement := readDocumentationFile(t, filepath.Join(root, "skills", "task-management", "SKILL.md"))
	loop := readDocumentationFile(t, filepath.Join(root, "skills", "codex-wtp-loop", "SKILL.md"))
	normalizedReadme := strings.Join(strings.Fields(readme), " ")
	normalizedLoop := strings.Join(strings.Fields(loop), " ")

	for _, field := range []string{"issueId", "project", "milestone", "version", "featureId", "feature"} {
		if !strings.Contains(readme, "`"+field+"`") {
			t.Errorf("README missing grouping field %q", field)
		}
		if !strings.Contains(taskManagement, "`"+field+"`") && !strings.Contains(taskManagement, field+"`") {
			t.Errorf("task-management skill missing grouping field %q", field)
		}
		if !strings.Contains(loop, "`"+field+"`") && !strings.Contains(loop, field+"`") {
			t.Errorf("codex-wtp-loop skill missing grouping field %q", field)
		}
	}

	for _, text := range []string{
		"featureId` is the stable",
		"feature` is its human-readable display name",
		"explicit empty assignment",
		"Legacy task files that omit",
		"case-insensitive exact strings",
		"AND semantics",
		"null` clears",
		"_clear",
		"all-or-nothing publication",
		"stats",
		"every automatic `task ready`/`task next`",
	} {
		if !strings.Contains(normalizedReadme, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("README missing grouping contract %q", text)
		}
	}

	for _, text := range []string{
		"grouping_scope",
		"wtp --json stats \"${grouping_scope[@]}\" model",
		"wtp --json task next --agent \"$agent_name\" \"${grouping_scope[@]}\"",
		"featureId` is the stable machine-facing feature key",
		"for one group and claim from an unrestricted",
		"same six-field selector values",
	} {
		if !strings.Contains(normalizedLoop, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("codex-wtp-loop skill missing targeted-loop contract %q", text)
		}
	}

	if !strings.Contains(changelog, "six optional grouping fields") || !strings.Contains(changelog, "targeted Codex WTP loops") {
		t.Error("CHANGELOG missing grouping and targeted-loop entries")
	}
}

func TestReusableDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	contributing := readDocumentationFile(t, filepath.Join(root, "CONTRIBUTING.md"))
	changelog := readDocumentationFile(t, filepath.Join(root, "CHANGELOG.md"))
	gitignore := readDocumentationFile(t, filepath.Join(root, ".gitignore"))
	normalizedReadme := strings.Join(strings.Fields(readme), " ")
	normalizedContributing := strings.Join(strings.Fields(contributing), " ")

	for _, text := range []string{
		"### Reusable advisory tasks",
		"wtp reusable create",
		"wtp reusable list",
		"wtp reusable show",
		"wtp reusable update",
		"wtp reusable delete",
		"case-insensitive name",
		"ordered canonical definition UUIDs",
		"live references",
		"reusableTaskIds",
		"reusableTasks",
		"Compact `task list` and `graph` output",
		"do not create queue items",
		"infer a group end",
		"tests_id",
		"review_id",
		"version_id",
		"commit_id",
		"last_task_id",
		"--reusable \"$tests_id\" --reusable \"$review_id\"",
		"--project Apollo --feature-id FEAT-7",
		"reusable.json",
		".wtp/meta/reusable-update.json",
		"prepared/committed",
		"backup",
		"all-or-nothing",
	} {
		if !strings.Contains(normalizedReadme, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("README missing reusable contract %q", text)
		}
	}

	for _, text := range []string{
		"### Reusable advisory definitions",
		"store-global advisory instructions",
		"repeatable `--reusable NAME_OR_ID`",
		"every status and branch scope",
		"reusableTaskIds",
		"reusableTasks",
		".wtp/reusable.json",
		".wtp/meta/reusable-update.json",
		"prepared journals roll back",
		"committed journals roll forward",
		"ignore the journal",
	} {
		if !strings.Contains(normalizedContributing, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("CONTRIBUTING missing reusable contract %q", text)
		}
	}

	if !strings.Contains(changelog, "store-global reusable advisory definitions") {
		t.Error("CHANGELOG missing reusable documentation entry")
	}
	for _, text := range []string{".wtp/meta/wtp.lock", ".wtp/meta/batch-update.json", ".wtp/meta/reusable-update.json"} {
		if !strings.Contains(gitignore, text) {
			t.Errorf(".gitignore missing transient reusable-storage entry %q", text)
		}
	}
}

func TestReusableSkillsDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	taskManagement := strings.Join(strings.Fields(readDocumentationFile(t, filepath.Join(root, "skills", "task-management", "SKILL.md"))), " ")
	loop := strings.Join(strings.Fields(readDocumentationFile(t, filepath.Join(root, "skills", "codex-wtp-loop", "SKILL.md"))), " ")
	setup := strings.Join(strings.Fields(readDocumentationFile(t, filepath.Join(root, "skills", "setup-wtp", "SKILL.md"))), " ")

	for _, text := range []string{
		"Reusable definitions are store-global advisory instructions",
		"wtp reusable create",
		"repeatable `--reusable NAME_OR_ID` flags",
		"explicitly choose the final parent task",
		"--reusable \"$tests_id\" --reusable \"$review_id\"",
		"exact stored order",
		"WTP neither executes nor enforces the instructions",
	} {
		if !strings.Contains(taskManagement, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("task-management skill missing reusable contract %q", text)
		}
	}

	for _, text := range []string{
		"resolved `reusableTasks` array",
		"explicit `task start` claim",
		"preserve their exact array order",
		"dedicated advisory block",
		"before reporting completion, address every resolved reusable advisory item",
		"WTP itself neither executes nor enforces them",
		"completion report addresses every resolved `reusableTasks` item in order",
	} {
		if !strings.Contains(loop, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("codex-wtp-loop skill missing reusable contract %q", text)
		}
	}

	for _, text := range []string{
		"durable `reusable.json` catalog",
		"complete source directory",
		"wtp export --out wtp-export-check",
		"complete version-1 `reusable.json` catalog",
		"transient `.wtp/meta/reusable-update.json`",
		"transient recovery journals",
		"ignore it alongside `wtp.lock` and `batch-update.json`",
	} {
		if !strings.Contains(setup, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("setup-wtp skill missing reusable contract %q", text)
		}
	}
}

func TestPlanningSkillsDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	skills := map[string]string{
		"task-management": strings.Join(strings.Fields(readDocumentationFile(t, filepath.Join(root, "skills", "task-management", "SKILL.md"))), " "),
		"codex-wtp-loop":  strings.Join(strings.Fields(readDocumentationFile(t, filepath.Join(root, "skills", "codex-wtp-loop", "SKILL.md"))), " "),
		"setup-wtp":       strings.Join(strings.Fields(readDocumentationFile(t, filepath.Join(root, "skills", "setup-wtp", "SKILL.md"))), " "),
	}

	for name, skill := range skills {
		if !strings.HasPrefix(skill, "--- name: "+name+" description:") {
			t.Errorf("%s skill is missing discoverable front matter", name)
		}
	}

	for _, text := range []string{
		"Manage future work in planning",
		"wtp planning report --project Apollo --version v2.0",
		"Create future work as a planning item",
		"stable grouping fields",
		"featureId` stable",
		"toplan -> researched | rejected",
		"researched -> toplan | planned | rejected",
		"planned -> researched | rejected",
		"wtp planning set-status \"$foundation_id\" researched",
		"--dry-run",
		"dependency-closed planned groups",
		"every reachable planning dependency",
		"never auto-adds missing planning dependencies",
		"`planning promote` requires at least one grouping selector",
		"rejected -> toplan",
		"ordinary task commands, stats, batch operations, and graph output remain planning-blind",
	} {
		if !strings.Contains(skills["task-management"], strings.Join(strings.Fields(text), " ")) {
			t.Errorf("task-management skill missing planning contract %q", text)
		}
	}

	for _, text := range []string{
		"Planning boundary: keep the execution loop planning-blind",
		"never claim planning work or auto-promote planning work",
		"Do not use this loop to inspect planning reports",
		"invoke `planning promote`",
		"dependency-closed planned group",
		"never pass it to `task start`",
	} {
		if !strings.Contains(skills["codex-wtp-loop"], strings.Join(strings.Fields(text), " ")) {
			t.Errorf("codex-wtp-loop skill missing planning boundary %q", text)
		}
	}

	for _, text := range []string{
		"complete `.wtp/planning/` tree",
		"toplan/",
		"researched/",
		"planned/",
		"rejected/",
		"test -d wtp-export-check/planning",
		"flat `planning/<planning-UUID>.json` records",
		".wtp/meta/planning-promote.json",
		"`batch-update.json`, then `reusable-update.json`, then `planning-promote.json`",
		"Prepared journals roll back and committed journals roll forward",
		"keeping `reusable.json`, the planning directory, and task directories tracked",
	} {
		if !strings.Contains(skills["setup-wtp"], strings.Join(strings.Fields(text), " ")) {
			t.Errorf("setup-wtp skill missing planning contract %q", text)
		}
	}
}

func TestPlanningDocumentationMatchesContract(t *testing.T) {
	root := repositoryRoot(t)
	readme := readDocumentationFile(t, filepath.Join(root, "README.md"))
	contributing := readDocumentationFile(t, filepath.Join(root, "CONTRIBUTING.md"))
	changelog := readDocumentationFile(t, filepath.Join(root, "CHANGELOG.md"))
	lifecycle := readDocumentationFile(t, filepath.Join(root, "docs", "planning-lifecycle.md"))
	cli := readDocumentationFile(t, filepath.Join(root, "internal", "cli", "cli.go"))
	normalizedReadme := strings.Join(strings.Fields(readme), " ")
	normalizedContributing := strings.Join(strings.Fields(contributing), " ")
	normalizedLifecycle := strings.Join(strings.Fields(lifecycle), " ")
	normalizedCLI := strings.Join(strings.Fields(cli), " ")

	for _, text := range []string{
		"### Versioned planning workflow",
		"planning/ toplan/<shortId>.json",
		"toplan -> researched | rejected",
		"researched -> toplan | planned | rejected",
		"planned -> researched | rejected",
		"rejected -> toplan",
		"`rejected` is the revisable replacement for deletion",
		"always have `startedAt: null` and `completedAt: null`",
		"featureId` is the stable machine-facing feature key",
		"feature` is its human-readable display name",
		"one UUID and short-ID namespace and one dependency graph",
		"normal `stats`, `batch`, `graph`, `ready`, and `next` exclude planning",
		"wtp planning create",
		"wtp planning list",
		"wtp planning show PLANNING-ID",
		"wtp planning update PLANNING-ID",
		"wtp planning set-status PLANNING-ID STATUS",
		"wtp planning report",
		"wtp planning promote",
		"case-insensitive exact string, and all supplied selectors must match (AND)",
		"projects[] -> versions[] -> milestones[]",
		"--dry-run",
		"dependency-closed",
		"planning-promote.json",
		"fixed recovery order: `batch-update.json` -> `reusable-update.json` -> `planning-promote.json`",
		"`planning/<planning-UUID>.json`",
		"Complete planning example",
		"planning set-status \"$foundation_id\" researched",
		"planning promote --project \"$project\" --version \"$version\"",
		"The only supported backend is flat-file storage",
	} {
		if !strings.Contains(normalizedReadme, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("README missing planning contract %q", text)
		}
	}

	for _, text := range []string{
		"### Versioned planning workflow",
		"flat-file-only",
		"`rejected` is the revisable replacement for deletion",
		"full task payload",
		"one UUID/short-ID namespace and one dependency graph",
		"normal task list/show/ready/next/start, stats, batch, and graph intentionally exclude planning",
		"complete task-create metadata flags",
		"six grouping selectors",
		"case-insensitive exact AND matches",
		"project -> version -> milestone",
		"dependency—also through executable vertices",
		"planning-promote.json",
		"`batch-update.json`, then `reusable-update.json`, then `planning-promote.json`",
		"flat `planning/<planning-UUID>.json` records",
		"only supported backend is the flat-file provider",
	} {
		if !strings.Contains(normalizedContributing, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("CONTRIBUTING missing planning contract %q", text)
		}
	}

	for _, text := range []string{
		"planning-promote.json",
		"Planning CLI and query contracts",
		"Report shape",
		"Dependency-closed promotion and preview",
		"Recovery order is fixed: batch-update -> reusable-update -> planning-promote",
		"Canonical export",
		"Planning export is flat",
		"Normal batch export remains planning-blind",
	} {
		if !strings.Contains(normalizedLifecycle, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("planning lifecycle contract missing %q", text)
		}
	}

	for _, text := range []string{
		"Versioned planning workflow:",
		"Planning command contract:",
		"Planning JSON, promotion, and recovery:",
		"Canonical planning export and compatibility:",
		"wtp planning create --title TITLE",
		"wtp planning set-status PLANNING-ID STATUS",
		"case-insensitively as an exact string",
		"this fixed order",
		"Planning export is flat inside the managed planning directory",
	} {
		if !strings.Contains(normalizedCLI, strings.Join(strings.Fields(text), " ")) {
			t.Errorf("CLI help/schema missing planning contract %q", text)
		}
	}

	if !strings.Contains(changelog, "Documented the versioned planning workflow") {
		t.Error("CHANGELOG missing planning documentation entry")
	}
}

func readDocumentationFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
