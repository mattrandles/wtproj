//go:build ignore

// release_fixture_server serves one local, GitHub-compatible release fixture.
// It is deliberately a standalone go run target so release_qa.sh can exercise
// the actual updater binary without publishing a release.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
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

// fixtureScenario is file-driven so release QA can replace it between cases
// without restarting the server or changing the candidate binary.
type fixtureScenario struct {
	LatestStatus   int            `json:"latestStatus"`
	LatestBody     string         `json:"latestBody"`
	LatestBodyFile string         `json:"latestBodyFile"`
	LatestDelayMS  int            `json:"latestDelayMs"`
	LatestRedirect string         `json:"latestRedirect"`
	AssetSet       bool           `json:"assetSet"`
	Assets         []fixtureAsset `json:"assets"`
}

type fixtureAsset struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	Body       string `json:"body"`
	BodyFile   string `json:"bodyFile"`
	DelayMS    int    `json:"delayMs"`
	Redirect   string `json:"redirect"`
	Close      bool   `json:"close"`
	TruncateAt int    `json:"truncateAt"`
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
		scenario, err := readScenario(*root)
		if err != nil {
			http.Error(writer, "invalid fixture scenario: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if scenario.LatestDelayMS > 0 {
			time.Sleep(time.Duration(scenario.LatestDelayMS) * time.Millisecond)
		}
		if scenario.LatestRedirect != "" {
			http.Redirect(writer, request, scenario.LatestRedirect, http.StatusFound)
			return
		}
		if scenario.LatestStatus != 0 && scenario.LatestStatus != http.StatusOK {
			writeScenarioResponse(writer, scenario.LatestStatus, scenario.LatestBody)
			return
		}
		latestBody := scenario.LatestBody
		if scenario.LatestBodyFile != "" {
			data, fileErr := os.ReadFile(filepath.Join(*root, scenario.LatestBodyFile))
			if fileErr != nil {
				http.Error(writer, "latest fixture body unavailable", http.StatusServiceUnavailable)
				return
			}
			latestBody = string(data)
		}
		if latestBody != "" {
			writeScenarioResponse(writer, http.StatusOK, latestBody)
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
		assetList := assetNames
		if scenario.AssetSet {
			assetList = make([]string, 0, len(scenario.Assets))
			for _, asset := range scenario.Assets {
				assetList = append(assetList, asset.Name)
			}
		}
		assets := make([]map[string]string, 0, len(assetList))
		for _, name := range assetList {
			downloadURL := baseURL + "/assets/" + name
			for _, asset := range scenario.Assets {
				if asset.Name == name && asset.URL != "" {
					downloadURL = asset.URL
					break
				}
			}
			assets = append(assets, map[string]string{"name": name, "browser_download_url": downloadURL})
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
		if scenario, err := readScenario(*root); err == nil {
			for _, asset := range scenario.Assets {
				if asset.Name == name {
					serveScenarioAsset(writer, *root, asset)
					return
				}
			}
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

func readScenario(root string) (fixtureScenario, error) {
	data, err := os.ReadFile(filepath.Join(root, "scenario.json"))
	if os.IsNotExist(err) {
		return fixtureScenario{}, nil
	}
	if err != nil {
		return fixtureScenario{}, err
	}
	var scenario fixtureScenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		return fixtureScenario{}, err
	}
	return scenario, nil
}

func writeScenarioResponse(writer http.ResponseWriter, status int, body string) {
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, body)
}

func serveScenarioAsset(writer http.ResponseWriter, root string, asset fixtureAsset) {
	if asset.DelayMS > 0 {
		time.Sleep(time.Duration(asset.DelayMS) * time.Millisecond)
	}
	if asset.Redirect != "" {
		writer.Header().Set("Location", asset.Redirect)
		writer.WriteHeader(http.StatusFound)
		return
	}
	if asset.Close {
		if hijacker, ok := writer.(http.Hijacker); ok {
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
				return
			}
		}
	}
	body := []byte(asset.Body)
	if asset.BodyFile != "" {
		data, err := os.ReadFile(filepath.Join(root, asset.BodyFile))
		if err != nil {
			http.Error(writer, "fixture asset unavailable", http.StatusServiceUnavailable)
			return
		}
		body = data
	}
	if asset.TruncateAt > 0 && asset.TruncateAt < len(body) {
		body = body[:asset.TruncateAt]
	}
	writeScenarioResponse(writer, asset.Status, string(body))
}

func isAsset(candidate string) bool {
	for _, name := range assetNames {
		if candidate == name {
			return true
		}
	}
	return false
}
