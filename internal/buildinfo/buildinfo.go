// Package buildinfo exposes metadata embedded in release builds.
package buildinfo

const (
	developmentVersion   = "dev"
	developmentCommit    = "none"
	developmentBuildDate = "unknown"
)

// These variables are set for release binaries with GoReleaser ldflags.
var (
	Version   = developmentVersion
	Commit    = developmentCommit
	BuildDate = developmentBuildDate
)

// Info is the version metadata reported by the CLI.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

// Current returns the release metadata, including explicit development defaults.
func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}
