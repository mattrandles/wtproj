//go:build wtp_fault_injection

package flatfile

import "os"

// faultPoint exists only in a QA binary built with -tags wtp_fault_injection.
// The environment value is never consulted by release/default binaries.
func faultPoint(point string) {
	if os.Getenv("WTP_FAULT_POINT") == point {
		os.Exit(97)
	}
}
