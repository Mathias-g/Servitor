// Command adrlint validates the decision log under docs/adr/:
//   - files are named NNNN-short-kebab-title.md, zero padded
//   - numbering is sequential and unique across accepted/proposed ADRs
//   - front matter parses and carries valid status, scope, and interface-impact
//
// It mirrors the ADR conventions in AGENTS.md and is wired into pre-commit and
// CI (see ADR-0006).
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	adrFileRe   = regexp.MustCompile(`^(\d{4})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	validStatus = map[string]bool{
		"proposed": true, "accepted": true,
		"deprecated": true, "superseded": true,
	}
	validImpact = map[string]bool{"none": true, "new": true, "breaking": true}
)

type adr struct {
	path   string
	num    int
	status string
	impact string
}

func main() {
	root := "docs/adr"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		fatal("cannot read %s: %v", root, err)
	}

	var adrs []*adr
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "README.md" {
			continue
		}
		if strings.HasPrefix(name, "0000-") {
			continue // 0000 is the template, not an ADR
		}
		if !adrFileRe.MatchString(name) {
			fmt.Printf("invalid ADR filename: %s (want NNNN-short-kebab-title.md)\n", name)
			os.Exit(1)
		}
		a := parseADRs(filepath.Join(root, name))
		adrs = append(adrs, a)
	}

	if err := checkSequential(adrs); err != nil {
		fatal("%v", err)
	}
	for _, a := range adrs {
		if a.status == "" {
			fatal("%s: missing or invalid status", a.path)
		}
		if a.impact == "" {
			fatal("%s: missing or invalid interface-impact", a.path)
		}
	}
	fmt.Printf("adrlint: %d ADRs OK\n", len(adrs))
}

func checkSequential(adrs []*adr) error {
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].num < adrs[j].num })
	for i, a := range adrs {
		want := i + 1 // 1-based: first ADR is 0001 (0000 is the template)
		if a.num != want {
			return fmt.Errorf("ADR numbering is not sequential: %s has number %04d, expected %04d", a.path, a.num, want)
		}
	}
	return nil
}

func parseADRs(path string) *adr {
	a := &adr{path: path}
	m := adrFileRe.FindStringSubmatch(filepath.Base(path))
	a.num, _ = strconv.Atoi(m[1])

	f, err := os.Open(path)
	if err != nil {
		fatal("cannot open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	inFront := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "---" && !inFront:
			inFront = true
			continue
		case line == "---" && inFront:
			return a
		case inFront:
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch k {
			case "status":
				if !validStatus[v] {
					fmt.Printf("%s: invalid status %q (want proposed|accepted|deprecated|superseded)\n", path, v)
					os.Exit(1)
				}
				a.status = v
			case "interface-impact":
				if !validImpact[v] {
					fmt.Printf("%s: invalid interface-impact %q (want none|new|breaking)\n", path, v)
					os.Exit(1)
				}
				a.impact = v
			}
		}
	}
	fatal("%s: missing closing '---' or no front matter", path)
	return nil
}

func fatal(format string, args ...interface{}) {
	fmt.Printf("adrlint: "+format+"\n", args...)
	os.Exit(1)
}
