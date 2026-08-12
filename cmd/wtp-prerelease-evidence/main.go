package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattrandles/wtproj/internal/prerelease"
)

type stringList []string

func (values *stringList) String() string         { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error { *values = append(*values, value); return nil }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "manifest":
		manifest(os.Args[2:])
	case "merge":
		merge(os.Args[2:])
	case "preflight":
		preflight(os.Args[2:])
	case "evaluate":
		evaluate(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func manifest(args []string) {
	flags := flag.NewFlagSet("manifest", flag.ExitOnError)
	directory := flags.String("candidate-dir", "", "flat directory containing six release assets and checksums.txt")
	out := flags.String("out", "", "candidate manifest output path")
	flags.Parse(args)
	if *directory == "" || *out == "" {
		fatal(errors.New("manifest requires --candidate-dir and --out"))
	}
	if _, err := prerelease.BuildCandidateManifest(*directory, *out); err != nil {
		fatal(err)
	}
}

func merge(args []string) {
	flags := flag.NewFlagSet("merge", flag.ExitOnError)
	manifestPath := flags.String("candidate-manifest", "", "candidate manifest")
	out := flags.String("out", "", "merged evidence output")
	updater := flags.String("updater-report", "", "release QA updater report")
	var shards, repeats stringList
	flags.Var(&shards, "shard", "native shard JSON; repeatable")
	flags.Var(&repeats, "repeat-report", "same-seed normalized comparison report; repeatable")
	flags.Parse(args)
	if *manifestPath == "" || *out == "" {
		fatal(errors.New("merge requires --candidate-manifest and --out"))
	}
	if _, err := prerelease.MergeEvidence(*manifestPath, *out, *updater, shards, repeats); err != nil {
		fatal(err)
	}
}

func preflight(args []string) {
	flags := flag.NewFlagSet("preflight", flag.ExitOnError)
	mergedPath := flags.String("merged", "", "merged evidence JSON")
	out := flags.String("out", "", "preflight JSON output")
	flags.Parse(args)
	var merged prerelease.MergedEvidence
	if err := read(*mergedPath, &merged); err != nil {
		fatal(err)
	}
	result := prerelease.PreflightEvidence(merged)
	if err := write(*out, result); err != nil {
		fatal(err)
	}
	if result.Status != "complete" {
		os.Exit(1)
	}
}

func evaluate(args []string) {
	flags := flag.NewFlagSet("evaluate", flag.ExitOnError)
	mergedPath := flags.String("merged", "", "merged evidence JSON")
	out := flags.String("out", "pre-release-verdict.md", "verdict markdown output")
	preflightPath := flags.String("preflight", "", "optional preflight JSON output")
	flags.Parse(args)
	var merged prerelease.MergedEvidence
	if err := read(*mergedPath, &merged); err != nil {
		fatal(err)
	}
	markdown, result := prerelease.EvaluateEvidence(merged, *mergedPath)
	if err := os.WriteFile(*out, []byte(markdown), 0o644); err != nil {
		fatal(err)
	}
	if *preflightPath != "" {
		if err := write(*preflightPath, result); err != nil {
			fatal(err)
		}
	}
	if result.Status != "complete" {
		os.Exit(1)
	}
}

func read(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return jsonUnmarshal(data, value)
}
func write(path string, value any) error {
	data, err := jsonMarshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
func jsonUnmarshal(data []byte, value any) error { return json.Unmarshal(data, value) }
func jsonMarshal(value any) ([]byte, error)      { return json.MarshalIndent(value, "", "  ") }
func fatal(err error)                            { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: wtp-prerelease-evidence {manifest|merge|preflight|evaluate} ...")
}
