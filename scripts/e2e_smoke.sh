#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
binary_path="$work_dir/wtp"
test_repo="$work_dir/repo"

cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

fail() {
  printf 'smoke test failed: %s\n' "$1" >&2
  exit 1
}

extract_json_string() {
  local key="$1"
  local file="$2"
  sed -n "s/.*\"${key}\": \"\\([^\"]*\\)\".*/\\1/p" "$file" | head -n 1
}

assert_contains() {
  local needle="$1"
  local file="$2"
  grep -Fq "$needle" "$file" || fail "expected '$needle' in $file"
}

mkdir -p "$test_repo"
git -C "$test_repo" init -q

go build -o "$binary_path" "$repo_root/cmd/wtp"

task_a_json="$work_dir/task_a.json"
task_b_json="$work_dir/task_b.json"
list_out="$work_dir/list.txt"
err_out="$work_dir/error.txt"
task_out="$work_dir/task.txt"
handoff_write_json="$work_dir/handoff_write.json"
handoff_get_json="$work_dir/handoff_get.json"
handoff_purge_json="$work_dir/handoff_purge.json"
handoff_after_purge_json="$work_dir/handoff_after_purge.json"
batch_export_path="$work_dir/batch_export.json"
batch_export_summary="$work_dir/batch_export_summary.txt"
batch_import_json="$work_dir/batch_import.json"

(
  cd "$test_repo"

  "$binary_path" help > /dev/null
  [ ! -e ".wtp" ] || fail "help initialized .wtp storage"
  "$binary_path" schema > /dev/null
  [ ! -e ".wtp" ] || fail "schema initialized .wtp storage"

  set +e
  "$binary_path" task show wtp-9999 --unknown > /dev/null 2> "$err_out"
  invalid_show_exit_code=$?
  set -e
  [ "$invalid_show_exit_code" -eq 1 ] || fail "unknown show option exited $invalid_show_exit_code, want 1"
  assert_contains 'unknown option "--unknown"' "$err_out"

  "$binary_path" --json task create \
    --title "Bootstrap provider" \
    --description "Initial task for smoke testing" \
    --issue-id ISSUE-42 \
    --project Apollo \
    --milestone MVP \
    --version v1 \
    --feature-id FEAT-7 \
    --feature Search > "$task_a_json"

  task_a_short_id="$(extract_json_string shortId "$task_a_json")"
  [ -n "$task_a_short_id" ] || fail "could not extract task A shortId"

  [ -f ".wtp/todo/${task_a_short_id}.json" ] || fail "task A filename was not written with shortId"

  "$binary_path" --json task edit "$task_a_short_id" --feature "Search Renamed" > "$task_a_json"
  assert_contains '"featureId": "FEAT-7"' "$task_a_json"
  assert_contains '"feature": "Search Renamed"' "$task_a_json"

  "$binary_path" --json task create \
    --title "Follow-up task" \
    --description "Depends on task A" \
    --depends-on "$task_a_short_id" \
    --issue-id ISSUE-42 \
    --project Apollo \
    --milestone MVP \
    --version v1 \
    --feature-id FEAT-7 \
    --feature "Search Renamed" > "$task_b_json"

  task_b_short_id="$(extract_json_string shortId "$task_b_json")"
  [ -n "$task_b_short_id" ] || fail "could not extract task B shortId"

  "$binary_path" task list > "$list_out"
  assert_contains "$task_a_short_id" "$list_out"
  assert_contains "$task_b_short_id" "$list_out"
  "$binary_path" task list --issue-id issue-42 --project apollo --milestone mvp --version V1 --feature-id feat-7 --feature "search renamed" > "$list_out"
  assert_contains "$task_a_short_id" "$list_out"
  assert_contains "$task_b_short_id" "$list_out"

  if "$binary_path" task start "$task_b_short_id" --agent Bob > /dev/null 2> "$err_out"; then
    fail "blocked dependency start unexpectedly succeeded"
  fi
  assert_contains "blocked by unresolved dependencies" "$err_out"

  "$binary_path" --json task next --agent Alice --issue-id issue-42 --project apollo --milestone mvp --version V1 --feature-id feat-7 --feature "search renamed" > "$task_out"
  assert_contains "\"shortId\": \"$task_a_short_id\"" "$task_out"
  assert_contains "\"status\": \"inProgress\"" "$task_out"
  assert_contains "\"assignee\": \"Alice\"" "$task_out"

  "$binary_path" task comment "$task_a_short_id" --agent Alice --message "smoke progress" > /dev/null
  "$binary_path" task done "$task_a_short_id" --agent Alice > /dev/null

  "$binary_path" --json --get-next-task --agent Bob --issue-id issue-42 --project apollo --milestone mvp --version V1 --feature-id feat-7 --feature "search renamed" > "$task_out"
  assert_contains "\"shortId\": \"$task_b_short_id\"" "$task_out"
  assert_contains "\"status\": \"inProgress\"" "$task_out"
  assert_contains "\"assignee\": \"Bob\"" "$task_out"

  "$binary_path" task pause "$task_b_short_id" > /dev/null
  "$binary_path" --json task next --agent Bob > "$task_out"
  assert_contains "\"shortId\": \"$task_b_short_id\"" "$task_out"
  assert_contains "\"status\": \"inProgress\"" "$task_out"

  "$binary_path" task done "$task_b_short_id" --agent Bob > /dev/null

  "$binary_path" batch export --out "$batch_export_path" --issue-id issue-42 --project apollo --milestone mvp --version V1 --feature-id feat-7 --feature "search renamed" > "$batch_export_summary"
  assert_contains 'taskCount: 2' "$batch_export_summary"
  "$binary_path" --json batch import --in "$batch_export_path" > "$batch_import_json"
  assert_contains '"updated": []' "$batch_import_json"
  assert_contains '"unchanged": [' "$batch_import_json"

  "$binary_path" export --out exported > /dev/null

  (
    cd "$repo_root"
    go run "$repo_root/scripts/assert_allocation_index.go" \
      --project-dir "$test_repo" \
      --store-dir .wtp \
      --task-id "$task_b_short_id" > /dev/null
  )
  [ -f "exported/$(extract_json_string id "$task_a_json").json" ] || fail "missing export for task A"
  [ -f "exported/$(extract_json_string id "$task_b_json").json" ] || fail "missing export for task B"

  "$binary_path" --json handoff write \
    --agent Alice \
    --message "handoff smoke context" > "$handoff_write_json"
  assert_contains '"message": "handoff smoke context"' "$handoff_write_json"

  "$binary_path" --json handoff get --all-scopes --all > "$handoff_get_json"
  assert_contains '"message": "handoff smoke context"' "$handoff_get_json"

  "$binary_path" --json handoff purge --global > "$handoff_purge_json"
  assert_contains '"purged": 1' "$handoff_purge_json"

  "$binary_path" --json handoff get --all-scopes --all > "$handoff_after_purge_json"
  assert_contains '"handoffs": []' "$handoff_after_purge_json"
)

printf 'smoke test passed\n'
