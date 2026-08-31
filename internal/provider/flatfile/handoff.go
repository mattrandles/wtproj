package flatfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattrandles/wtproj/internal/core"
	"github.com/mattrandles/wtproj/internal/provider"
)

const handoffsFilename = "handoffs.json"

var canonicalHandoffIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type handoffFile struct {
	Handoffs []core.Handoff `json:"handoffs"`
}

func (p *Provider) handoffsPath() string {
	return filepath.Join(p.root, handoffsFilename)
}

// WriteHandoff retains a new global or task-scoped handoff. Replacing affects
// only the selected scope; records from every other scope are preserved.
func (p *Provider) WriteHandoff(request provider.HandoffWriteRequest) (provider.HandoffWriteResult, error) {
	var result provider.HandoffWriteResult
	err := p.withGlobalLock(func() error {
		snapshot, err := p.loadValidationSnapshot(nil)
		if err != nil {
			return err
		}
		taskID, err := resolveHandoffTaskID(request.Task, snapshot.tasks)
		if err != nil {
			return err
		}

		id, err := core.NewID()
		if err != nil {
			return err
		}
		handoff := core.Handoff{
			ID:        id,
			TaskID:    taskID,
			Author:    strings.TrimSpace(request.Author),
			Message:   strings.TrimSpace(request.Message),
			CreatedAt: time.Now().UTC(),
		}
		if err := handoff.Validate(); err != nil {
			return err
		}

		handoffs, err := p.loadHandoffs()
		if err != nil {
			return err
		}
		if request.Replace {
			retained := make([]core.Handoff, 0, len(handoffs)+1)
			for _, existing := range handoffs {
				if existing.TaskID != taskID {
					retained = append(retained, existing)
				}
			}
			handoffs = retained
		}
		handoffs = append(handoffs, handoff)
		if err := p.writeHandoffs(handoffs); err != nil {
			return err
		}

		result = provider.HandoffWriteResult{
			Handoff:    handoff,
			ScopeCount: countHandoffScope(handoffs, taskID),
		}
		return nil
	})
	return result, err
}

// ListHandoffs returns retained handoffs in newest-first order. With no task
// and AllScopes false it selects global handoffs only.
func (p *Provider) ListHandoffs(filter provider.HandoffFilter) (provider.HandoffListResult, error) {
	var result provider.HandoffListResult
	err := p.withGlobalLock(func() error {
		if filter.Limit < 0 {
			return errors.New("handoff limit cannot be negative")
		}
		if filter.AllScopes && strings.TrimSpace(filter.Task) != "" {
			return errors.New("handoff task filter cannot be combined with all scopes")
		}

		snapshot, err := p.loadValidationSnapshot(nil)
		if err != nil {
			return err
		}
		taskID, err := resolveHandoffTaskID(filter.Task, snapshot.tasks)
		if err != nil {
			return err
		}
		handoffs, err := p.loadHandoffs()
		if err != nil {
			return err
		}

		matching := make([]core.Handoff, 0, len(handoffs))
		otherScopesAvailable := false
		for _, handoff := range handoffs {
			if isPlanningHandoff(handoff, snapshot.planningItems) {
				continue
			}
			if filter.AllScopes || handoff.TaskID == taskID {
				matching = append(matching, handoff)
				continue
			}
			otherScopesAvailable = true
		}
		sort.Slice(matching, func(i, j int) bool {
			if matching[i].CreatedAt.Equal(matching[j].CreatedAt) {
				return matching[i].ID < matching[j].ID
			}
			return matching[i].CreatedAt.After(matching[j].CreatedAt)
		})

		result.TotalMatching = len(matching)
		result.OtherScopesAvailable = otherScopesAvailable
		if filter.Limit > 0 && len(matching) > filter.Limit {
			result.Handoffs = matching[:filter.Limit]
			result.HasMore = true
			return nil
		}
		result.Handoffs = matching
		return nil
	})
	return result, err
}

// PurgeHandoffs deletes handoffs selected by one exact ID or one scope. A
// cutoff, when supplied, retains handoffs created at or after that instant.
func (p *Provider) PurgeHandoffs(request provider.HandoffPurgeRequest) (provider.HandoffPurgeResult, error) {
	var result provider.HandoffPurgeResult
	request.Task = strings.TrimSpace(request.Task)
	err := p.withGlobalLock(func() error {
		snapshot, err := p.loadValidationSnapshot(nil)
		if err != nil {
			return err
		}
		selector, taskID, err := resolveHandoffPurgeSelector(request, snapshot.tasks)
		if err != nil {
			return err
		}

		handoffs, err := p.loadHandoffs()
		if err != nil {
			return err
		}

		retained := make([]core.Handoff, 0, len(handoffs))
		foundID := false
		for _, handoff := range handoffs {
			// Planning-scoped handoffs are outside the execution lifecycle. Keep
			// them in storage, but never expose or mutate them through execution
			// handoff operations. Unknown task scopes remain recoverable orphan
			// records for compatibility with the existing purge-by-ID contract.
			if isPlanningHandoff(handoff, snapshot.planningItems) {
				retained = append(retained, handoff)
				continue
			}
			matches := false
			switch selector {
			case handoffPurgeByID:
				matches = handoff.ID == request.ID
				foundID = foundID || matches
			case handoffPurgeGlobal:
				matches = handoff.TaskID == ""
			case handoffPurgeTask:
				matches = handoff.TaskID == taskID
			case handoffPurgeAllScopes:
				matches = true
			}

			if matches && (request.Before == nil || handoff.CreatedAt.Before(*request.Before)) {
				result.Purged++
				continue
			}
			retained = append(retained, handoff)
		}
		if selector == handoffPurgeByID && !foundID {
			return fmt.Errorf("handoff %q not found", request.ID)
		}
		if result.Purged == 0 {
			return nil
		}
		return p.writeHandoffs(retained)
	})
	return result, err
}

type handoffPurgeSelector int

const (
	handoffPurgeByID handoffPurgeSelector = iota
	handoffPurgeGlobal
	handoffPurgeTask
	handoffPurgeAllScopes
)

func resolveHandoffPurgeSelector(request provider.HandoffPurgeRequest, tasks []core.Task) (handoffPurgeSelector, string, error) {
	request.Task = strings.TrimSpace(request.Task)
	selectors := 0
	if request.ID != "" {
		selectors++
	}
	if request.Global {
		selectors++
	}
	if request.Task != "" {
		selectors++
	}
	if request.AllScopes {
		selectors++
	}
	if selectors != 1 {
		return 0, "", errors.New("handoff purge requires exactly one selector: id, global, task, or all scopes")
	}
	if request.ID != "" {
		if !canonicalHandoffIDPattern.MatchString(request.ID) {
			return 0, "", fmt.Errorf("handoff id %q must be a canonical lowercase UUID", request.ID)
		}
		return handoffPurgeByID, "", nil
	}
	if request.Global {
		return handoffPurgeGlobal, "", nil
	}
	if request.AllScopes {
		return handoffPurgeAllScopes, "", nil
	}

	taskID, err := resolveHandoffTaskID(request.Task, tasks)
	if err != nil {
		return 0, "", err
	}
	return handoffPurgeTask, taskID, nil
}

func resolveHandoffTaskID(idOrShortID string, tasks []core.Task) (string, error) {
	idOrShortID = strings.TrimSpace(idOrShortID)
	if idOrShortID == "" {
		return "", nil
	}
	task, err := resolveTask(idOrShortID, tasks)
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

func isPlanningHandoff(handoff core.Handoff, planningItems []core.PlanningItem) bool {
	if handoff.TaskID == "" {
		return false
	}
	for _, item := range planningItems {
		if item.ID == handoff.TaskID {
			return true
		}
	}
	return false
}

func countHandoffScope(handoffs []core.Handoff, taskID string) int {
	count := 0
	for _, handoff := range handoffs {
		if handoff.TaskID == taskID {
			count++
		}
	}
	return count
}

// taskHandoffs selects retained context for one task in newest-first order.
// Handoff storage is validated by loadHandoffs before this function is called.
func taskHandoffs(handoffs []core.Handoff, taskID string) []core.Handoff {
	matching := make([]core.Handoff, 0)
	for _, handoff := range handoffs {
		if handoff.TaskID == taskID {
			matching = append(matching, handoff)
		}
	}
	sort.Slice(matching, func(i, j int) bool {
		if matching[i].CreatedAt.Equal(matching[j].CreatedAt) {
			return matching[i].ID < matching[j].ID
		}
		return matching[i].CreatedAt.After(matching[j].CreatedAt)
	})
	return matching
}

// loadHandoffs reads and validates the complete retained-handoff collection.
// Provider operations call it while holding the global lock. A missing file is
// a legacy store with no retained handoffs.
func (p *Provider) loadHandoffs() ([]core.Handoff, error) {
	path := p.handoffsPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []core.Handoff{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read handoff file %s: %w", path, err)
	}

	var stored handoffFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("corrupt handoff file %s: %w", path, err)
	}
	if stored.Handoffs == nil {
		return nil, fmt.Errorf("invalid handoff file %s: handoffs must be an array", path)
	}
	if err := validateHandoffs(stored.Handoffs); err != nil {
		return nil, fmt.Errorf("invalid handoff file %s: %w", path, err)
	}
	return stored.Handoffs, nil
}

// writeHandoffs validates and atomically publishes the complete collection.
// Provider operations call it while holding the global lock so read-modify-
// write sequences cannot lose concurrent updates.
func (p *Provider) writeHandoffs(handoffs []core.Handoff) error {
	if err := validateHandoffs(handoffs); err != nil {
		return fmt.Errorf("invalid handoff collection: %w", err)
	}
	if handoffs == nil {
		handoffs = []core.Handoff{}
	}
	if err := p.writeJSONAtomic(p.handoffsPath(), handoffFile{Handoffs: handoffs}); err != nil {
		return err
	}
	faultPoint("handoff-replacement")
	return nil
}

func validateHandoffs(handoffs []core.Handoff) error {
	ids := make(map[string]struct{}, len(handoffs))
	for index, handoff := range handoffs {
		if err := handoff.Validate(); err != nil {
			return fmt.Errorf("handoff %d: %w", index, err)
		}
		if _, exists := ids[handoff.ID]; exists {
			return fmt.Errorf("handoff id %s is duplicated", handoff.ID)
		}
		ids[handoff.ID] = struct{}{}
	}
	return nil
}
