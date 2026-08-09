package core

import (
	"crypto/sha256"
	"encoding/hex"
)

// BranchScope identifies the task namespace for a named Git branch.
//
// Branch is the exact case-sensitive short branch name reported by Git.
// BranchID is the first 32 bits of that name's SHA-256 digest, encoded as
// eight lowercase hexadecimal characters.
type BranchScope struct {
	Branch   string
	BranchID string
}

// BranchID returns the deterministic 32-bit identity for branch.
func BranchID(branch string) string {
	digest := sha256.Sum256([]byte(branch))
	return hex.EncodeToString(digest[:4])
}

// NewBranchScope returns the scope for a named branch. An empty branch name
// represents detached or non-Git execution and has no scope.
func NewBranchScope(branch string) *BranchScope {
	if branch == "" {
		return nil
	}
	return &BranchScope{
		Branch:   branch,
		BranchID: BranchID(branch),
	}
}
