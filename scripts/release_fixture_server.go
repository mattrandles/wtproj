//go:build ignore

// release_fixture_server serves one local, GitHub-compatible release fixture.
// It is deliberately a standalone go run target so release_qa.sh can exercise
// the actual updater binary without publishing a release.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

var assetNames = []string{
	"wtp_darwin_amd64",
	"wtp_darwin_arm64",
	"wtp_linux_amd64",
	"wtp_linux_arm64",
	"wtp_windows_amd64.exe",
	"wtp_windows_arm64.exe",
	"checksums.txt",
}

func main() {
	root := flag.String("root", "", "directory containing release assets and latest.json")
	readyFile := flag.String("ready-file", "", "file to write with the fixture base URL")
	flag.Parse()
	if *root == "" || *readyFile == "" {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/release_fixture_server.go --root DIR --ready-file FILE")
		os.Exit(2)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	baseURL := "http://" + listener.Addr().String()
	if err := os.WriteFile(*readyFile, []byte(baseURL), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write ready file: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/mattrandles/wtproj/releases/latest", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(writer, "GET required", http.StatusMethodNotAllowed)
			return
		}
		if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			http.Error(writer, "missing GitHub API headers", http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(filepath.Join(*root, "latest.json"))
		if err != nil {
			http.Error(writer, "release fixture is not ready", http.StatusServiceUnavailable)
			return
		}
		var release struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(data, &release); err != nil || release.TagName == "" {
			http.Error(writer, "invalid release fixture", http.StatusInternalServerError)
			return
		}
		assets := make([]map[string]string, 0, len(assetNames))
		for _, name := range assetNames {
			assets = append(assets, map[string]string{
				"name":                 name,
				"browser_download_url": baseURL + "/assets/" + name,
			})
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"tag_name": release.TagName, "assets": assets})
	})
	mux.HandleFunc("/assets/", func(writer http.ResponseWriter, request *http.Request) {
		name := filepath.Base(request.URL.Path)
		if request.Method != http.MethodGet || !isAsset(name) {
			http.NotFound(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(*root, name))
	})
	mux.HandleFunc("/initial-assets/", func(writer http.ResponseWriter, request *http.Request) {
		name := filepath.Base(request.URL.Path)
		if request.Method != http.MethodGet || !isAsset(name) {
			http.NotFound(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(*root, "initial", name))
	})

	if err := http.Serve(listener, mux); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "serve fixture: %v\n", err)
		os.Exit(1)
	}
}

func isAsset(candidate string) bool {
	for _, name := range assetNames {
		if candidate == name {
			return true
		}
	}
	return false
}
