package flatfile

// These declarations define the planning storage and promotion transaction
// format shared by the provider implementation and its recovery path.
const (
	planningDirectory             = "planning"
	planningPromoteJournalName    = "planning-promote.json"
	planningPromoteJournalVersion = 1
	planningPromotePrepared       = "prepared"
	planningPromoteCommitted      = "committed"
)

// planningRecoveryJournalOrder declares the complete v1 recovery order. The
// recovery implementation must preflight all pending journals for conflicting
// identities/paths before any writes, then recover in this order under the
// global lock. Do not reorder based on directory or timestamp sorting.
func planningRecoveryJournalOrder() []string {
	return []string{batchUpdateJournalName, reusableUpdateJournalName, planningPromoteJournalName}
}

// planningPromoteJournal stores exact before/after endpoints in deterministic
// CreatedAt/ShortID selection order. SelectedIDs contains canonical UUIDs in
// one-to-one agreement with Entries. Both arrays are required and non-empty.
type planningPromoteJournal struct {
	Version     int                           `json:"version"`
	State       string                        `json:"state"`
	SelectedIDs []string                      `json:"selectedIds"`
	Entries     []planningPromoteJournalEntry `json:"entries"`
}

type planningPromoteJournalEntry struct {
	Before planningPromoteSnapshot `json:"before"`
	After  planningPromoteSnapshot `json:"after"`
}

// Paths are canonical slash-separated paths relative to the store, never
// absolute/native paths. Before is planning/planned/<shortId-or-UUID>.json;
// After is todo/<shortId>.json. Data is required non-empty base64 JSON bytes,
// not decoded/re-encoded objects. Each endpoint denotes an existing snapshot;
// recovery removes the opposite endpoint (no Exists flag is needed).
type planningPromoteSnapshot struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}
