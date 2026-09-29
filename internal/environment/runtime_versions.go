package environment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

var versionToken = regexp.MustCompile(`\d+(?:\.\d+){0,2}`)

// applyDetectedVersions records where each suggestion came from. CI wins over
// project hints because it is the project's independent verification target.
// Only local, bounded files are read; their contents never become image names.
func applyDetectedVersions(root string, runtime *domain.RuntimeSpec) {
	if runtime.VersionSources == nil {
		runtime.VersionSources = map[string]string{}
	}
	choose := func(tool, value, source string, priority bool) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		current := runtime.Toolchains[tool]
		if current == "" {
			// Package managers may be identified even when the language manifest
			// was not recognized, but only if their own manifest supplied a hint.
			runtime.Toolchains[tool] = value
			runtime.VersionSources[tool] = source
			return
		}
		if runtime.VersionSources[tool] == "Point default" {
			runtime.Toolchains[tool] = value
			runtime.VersionSources[tool] = source
			return
		}
		if !sameVersion(current, value) {
			runtime.VersionConflicts = append(runtime.VersionConflicts, tool+": "+current+" ("+runtime.VersionSources[tool]+") / "+value+" ("+source+")")
		}
		if priority {
			runtime.Toolchains[tool] = value
			runtime.VersionSources[tool] = source
		}
	}
	projectVersionHints(root, choose)
	ciVersionHints(root, choose)
	sort.Strings(runtime.VersionConflicts)
}

func sameVersion(left, right string) bool {
	a, b := versionToken.FindString(left), versionToken.FindString(right)
	return a != "" && b != "" && (a == b || strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+"."))
}

func versionFile(root, name string) string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || len(data) > 256*1024 {
		return ""
	}
	return string(data)
}

func firstVersion(value string) string { return versionToken.FindString(value) }

func projectVersionHints(root string, choose func(string, string, string, bool)) {
	for _, item := range []struct{ file, tool string }{
		{".nvmrc", "node"}, {".node-version", "node"}, {".python-version", "python"},
	} {
		if value := firstVersion(versionFile(root, item.file)); value != "" {
			choose(item.tool, value, item.file, false)
		}
	}
	if raw := versionFile(root, ".tool-versions"); raw != "" {
		for _, line := range strings.Split(raw, "\n") {
			parts := strings.Fields(line)
			if len(parts) < 2 || strings.HasPrefix(parts[0], "#") {
				continue
			}
			tool := map[string]string{"nodejs": "node", "python": "python", "golang": "go", "java": "java", "rust": "rust", "dotnet": "dotnet", "php": "php", "maven": "mvn", "gradle": "gradle", "composer": "composer"}[parts[0]]
			if tool != "" {
				choose(tool, firstVersion(parts[1]), ".tool-versions", false)
			}
		}
	}
	if raw := versionFile(root, "package.json"); raw != "" {
		var manifest struct {
			Engines        map[string]string `json:"engines"`
			PackageManager string            `json:"packageManager"`
		}
		if json.Unmarshal([]byte(raw), &manifest) == nil {
			choose("node", firstVersion(manifest.Engines["node"]), "package.json engines", false)
			for _, tool := range []string{"npm", "pnpm", "yarn"} {
				choose(tool, firstVersion(manifest.Engines[tool]), "package.json engines", false)
				if strings.HasPrefix(manifest.PackageManager, tool+"@") {
					choose(tool, firstVersion(manifest.PackageManager), "package.json packageManager", false)
				}
			}
		}
	}
	if raw := versionFile(root, "composer.json"); raw != "" {
		var manifest struct {
			Require map[string]string `json:"require"`
			Config struct { Platform map[string]string `json:"platform"` } `json:"config"`
		}
		if json.Unmarshal([]byte(raw), &manifest) == nil {
			choose("php", firstVersion(manifest.Require["php"]), "composer.json require.php", false)
			choose("php", firstVersion(manifest.Config.Platform["php"]), "composer.json platform.php", false)
		}
	}
	if raw := versionFile(root, "go.mod"); raw != "" {
		choose("go", captureVersion(raw, `(?m)^go\s+(\d+(?:\.\d+){1,2})`), "go.mod", false)
		choose("go", captureVersion(raw, `(?m)^toolchain\s+go(\d+(?:\.\d+){1,2})`), "go.mod toolchain", false)
	}
	if raw := versionFile(root, "pyproject.toml"); raw != "" {
		choose("python", captureVersion(raw, `(?m)^requires-python\s*=\s*["'][^\d]*(\d+(?:\.\d+){1,2})`), "pyproject.toml", false)
	}
	if raw := versionFile(root, "rust-toolchain.toml"); raw != "" {
		choose("rust", captureVersion(raw, `(?m)^channel\s*=\s*["']([^"']+)`), "rust-toolchain.toml", false)
	}
	if raw := versionFile(root, "Cargo.toml"); raw != "" {
		choose("rust", captureVersion(raw, `(?m)^rust-version\s*=\s*["'](\d+(?:\.\d+){0,2})`), "Cargo.toml", false)
	}
	if raw := versionFile(root, "requirements.txt"); raw != "" {
		choose("pip", captureVersion(raw, `(?m)^pip==\s*(\d+(?:\.\d+){0,2})`), "requirements.txt", false)
	}
	if raw := versionFile(root, "gradle/wrapper/gradle-wrapper.properties"); raw != "" {
		choose("gradle", captureVersion(raw, `gradle-(\d+(?:\.\d+){0,2})-(?:bin|all)\.zip`), "gradle-wrapper.properties", false)
	}
	if raw := versionFile(root, ".mvn/wrapper/maven-wrapper.properties"); raw != "" {
		choose("mvn", captureVersion(raw, `apache-maven-(\d+(?:\.\d+){0,2})-bin`), "maven-wrapper.properties", false)
	}
	if raw := versionFile(root, "global.json"); raw != "" {
		var manifest struct {
			SDK struct {
				Version string `json:"version"`
			} `json:"sdk"`
		}
		if json.Unmarshal([]byte(raw), &manifest) == nil {
			choose("dotnet", manifest.SDK.Version, "global.json", false)
		}
	}
	if raw := versionFile(root, "pom.xml"); raw != "" {
		choose("java", captureVersion(raw, `<maven\.compiler\.release>\s*(\d+)\s*</maven\.compiler\.release>`), "pom.xml", false)
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		if raw := versionFile(root, name); raw != "" {
			choose("java", captureVersion(raw, `JavaVersion\.VERSION_(\d+)`), name, false)
		}
	}
}

func ciVersionHints(root string, choose func(string, string, string, bool)) {
	files := []string{".gitlab-ci.yml", ".gitlab-ci.yaml"}
	for _, pattern := range []string{".github/workflows/*.yml", ".github/workflows/*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		for _, name := range matches {
			files = append(files, strings.TrimPrefix(filepath.ToSlash(name), filepath.ToSlash(root)+"/"))
		}
	}
	for _, name := range files {
		raw := versionFile(root, name)
		if raw == "" {
			continue
		}
		for _, item := range []struct{ tool, pattern string }{
			{"node", `(?m)\bimage:\s*["']?node:(\d+(?:\.\d+){0,2})`},
			{"node", `(?m)\bnode-version:\s*["']?(\d+(?:\.\d+){0,2})`},
			{"python", `(?m)\bpython-version:\s*["']?(\d+(?:\.\d+){0,2})`},
			{"python", `(?m)\bimage:\s*["']?python:(\d+(?:\.\d+){0,2})`},
			{"go", `(?m)\bgo-version:\s*["']?(\d+(?:\.\d+){0,2})`},
			{"go", `(?m)\bimage:\s*["']?golang:(\d+(?:\.\d+){0,2})`},
			{"php", `(?m)\bimage:\s*["']?php:(\d+(?:\.\d+){0,2})`},
			{"php", `(?m)\bphp-version:\s*["']?(\d+(?:\.\d+){0,2})`},
			{"composer", `(?m)\bimage:\s*["']?composer:(\d+(?:\.\d+){0,2})`},
			{"rust", `(?m)\bimage:\s*["']?rust:(\d+(?:\.\d+){0,2})`},
			{"mvn", `(?m)\bimage:\s*["']?maven:(\d+(?:\.\d+){0,2})`},
			{"gradle", `(?m)\bimage:\s*["']?gradle:(\d+(?:\.\d+){0,2})`},
			{"java", `(?m)\bjava-version:\s*["']?(\d+)`},
			{"dotnet", `(?m)\bdotnet-version:\s*["']?(\d+(?:\.\d+){0,2})`},
		} {
			if value := captureVersion(raw, item.pattern); value != "" {
				choose(item.tool, value, name, true)
			}
		}
	}
}

func captureVersion(value, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(value)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
