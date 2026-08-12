//go:build wtp_fault_injection

package updater

import "os"

func faultPoint(point string) {
	if os.Getenv("WTP_FAULT_POINT") == point {
		os.Exit(97)
	}
}
