// Synchronize the two top-level Helm version fields without rewriting YAML.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const chart = "deploy/helm/agentiplex/Chart.yaml"
const semver = "(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)"

func syncVersion(mode, version string) error {
	if !regexp.MustCompile("^"+semver+"$").MatchString(version) {
		return fmt.Errorf("invalid stable version: %s", version)
	}
	raw, err := os.ReadFile(chart)
	if err != nil { return err }
	lines := strings.SplitAfter(string(raw), "\n")
	for _, key := range []string{"version", "appVersion"} {
		field := regexp.MustCompile("^"+key+"[ \t]*:")
		value := regexp.MustCompile("^("+key+":[ \t]*)(?:("+semver+")|\"("+semver+")\"|'("+semver+")')([ \t]*(?:#[^\n]*)?)(\n?)$")
		count := 0
		for i, line := range lines {
			if !field.MatchString(line) { continue }
			count++
			m := value.FindStringSubmatch(line)
			if m == nil { return fmt.Errorf("%s: invalid stable %s", chart, key) }
			old := m[2]+m[3]+m[4]
			if mode == "check" && old != version {
				return fmt.Errorf("%s: %s is %s, expected %s", chart, key, old, version)
			}
			if mode == "preview" { fmt.Printf("%s: %s %s -> %s\n", chart, key, old, version) }
			next := version
			if key == "appVersion" { next = "\""+version+"\"" }
			lines[i] = m[1]+next+m[5]+m[6]
		}
		if count != 1 { return fmt.Errorf("%s: expected exactly one top-level %s", chart, key) }
	}
	if updated := strings.Join(lines, ""); mode == "update" && updated != string(raw) {
		return os.WriteFile(chart, []byte(updated), 0644)
	}
	return nil
}

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "preview" && os.Args[1] != "update" && os.Args[1] != "check") {
		fmt.Fprintln(os.Stderr, "usage: chart-version.go preview|update|check MAJOR.MINOR.PATCH")
		os.Exit(1)
	}
	if err := syncVersion(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
