package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mattrandles/wtproj/internal/prerelease"
)

func main() {
	flags := flag.NewFlagSet("wtp-prerelease-qa", flag.ExitOnError)
	candidate := flags.String("candidate", "", "explicit candidate wtp executable (required; never resolved from PATH)")
	candidateAsset := flags.String("candidate-asset", "", "exact release asset filename used by this native shard")
	seed := flags.Int64("seed", 1, "deterministic scenario seed")
	repeat := flags.Int("repeat", 20, "number of complete matrix repetitions")
	reportPath := flags.String("report", "", "versioned JSON report path")
	keepWorkdir := flags.Bool("keep-workdir", false, "retain the disposable fixture root")
	timeout := flags.Duration("timeout", 30*time.Second, "per-command timeout")
	suiteTimeout := flags.Duration("suite-timeout", 2*time.Minute, "per-scenario contention suite timeout")
	sourceRoot := flags.String("source-root", "", "source checkout to manifest (defaults to current directory)")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	report, err := prerelease.Run(prerelease.Options{Candidate: *candidate, CandidateAsset: *candidateAsset, Seed: *seed, Repeat: *repeat, Report: *reportPath, KeepWorkdir: *keepWorkdir, Timeout: *timeout, SuiteTimeout: *suiteTimeout, SourceRoot: *sourceRoot})
	if err != nil {
		fmt.Fprintf(os.Stderr, "prerelease QA failed: %v\n", err)
		if report.Reproduction != "" {
			fmt.Fprintf(os.Stderr, "reproduce: %s\n", report.Reproduction)
		}
		fmt.Printf("prerelease QA: %s (%d scenarios, seed %d)\n", report.Status, len(report.Scenarios), report.Seed)
		os.Exit(1)
	}
	fmt.Printf("prerelease QA: %s (%d scenarios, seed %d, candidate %s)\n", report.Status, len(report.Scenarios), report.Seed, report.Candidate.SHA256)
}
