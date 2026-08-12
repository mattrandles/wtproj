//go:build !wtp_fault_injection

package flatfile

// faultPoint is intentionally a no-op in every normal and release build.
func faultPoint(string) {}
