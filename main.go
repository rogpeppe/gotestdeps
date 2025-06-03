// Command gomodviz prints a GraphViz “dot” graph of the current module’s
// dependencies, colouring modules required only by tests in red.
//
//	go run . > deps.mmd
//
// Requires: go1.22+ and golang.org/x/tools/go/packages.
package main

import (
	"container/list"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	testColor    = "#ffdddd"
	nonTestColor = "#ececff"
	mainColor    = "#ddffdd"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gotestdeps\n")
		fmt.Fprintf(os.Stderr, `
Command gotestdeps prints the Go module dependency graph, highlighting
in red the modules that are present only because of tests.
`)
	}
	flag.Parse()

	// 1. Load the module universe twice: with and without test files.
	mainMod, _, noTestMods := loadModuleSet(false, "all")
	_, testPkgs, withTestMods := loadModuleSet(true, "all")

	// 2. Any module present only in the second load is “test-only”.
	testOnly := difference(withTestMods, noTestMods)

	// 3. Derive module-to-module edges from the test-inclusive graph.
	edges, nodes := buildEdges(testPkgs)

	// Ensure pure test nodes without outgoing edges still appear.
	for m := range testOnly {
		nodes[m] = struct{}{}
	}

	// 4. Emit GraphViz.
	writeDot(os.Stdout, mainMod, edges, nodes, testOnly)
}

func loadModuleSet(includeTests bool, pattern string) (string, []*packages.Package, map[string]struct{}) {
	cfg := &packages.Config{
		Mode:  packages.NeedImports | packages.NeedModule | packages.NeedDeps,
		Tests: includeTests,
	}
	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		log.Fatalf("packages.Load (Tests=%v): %v", includeTests, err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		log.Fatal("aborting due to previous errors")
	}

	mods := make(map[string]struct{})
	mainMod := ""
	traverse(pkgs, func(p *packages.Package) {
		if p.Module != nil {
			mods[p.Module.Path] = struct{}{}
			if p.Module.Main {
				mainMod = p.Module.Path
			}
		}
	})
	return mainMod, pkgs, mods
}

// traverse walks the import graph once, visiting every package exactly once.
func traverse(roots []*packages.Package, visit func(*packages.Package)) {
	seen := make(map[*packages.Package]bool)
	q := list.New()
	for _, p := range roots {
		q.PushBack(p)
	}
	for q.Len() > 0 {
		p := q.Remove(q.Back()).(*packages.Package)
		if seen[p] {
			continue
		}
		seen[p] = true
		visit(p)
		for _, imp := range p.Imports {
			if imp != nil {
				q.PushBack(imp)
			}
		}
	}
}

func buildEdges(pkgs []*packages.Package) (map[string]map[string]struct{}, map[string]struct{}) {
	edges := make(map[string]map[string]struct{})
	nodes := make(map[string]struct{})
	traverse(pkgs, func(p *packages.Package) {
		from := modulePathOf(p)
		if from == "" {
			return // stdlib
		}
		nodes[from] = struct{}{}
		for _, imp := range p.Imports {
			to := modulePathOf(imp)
			if to == "" || to == from {
				continue
			}
			if edges[from] == nil {
				edges[from] = make(map[string]struct{})
			}
			edges[from][to] = struct{}{}
			nodes[to] = struct{}{}
		}
	})
	return edges, nodes
}

func modulePathOf(p *packages.Package) string {
	if p != nil && p.Module != nil {
		return p.Module.Path // omit version; GraphViz node ≡ module path
	}
	return ""
}

func writeDot(out io.Writer, mainMod string, edges map[string]map[string]struct{},
	nodes, testOnly map[string]struct{}) {

	fmt.Fprintf(out, "```mermaid\n")
	fmt.Fprintf(out, "graph LR\n")
	//	fmt.Fprint(out, `
	//digraph G {
	//    node [shape=rectangle target="_graphviz"];
	//    edge [tailport=e];
	//    compound=true;
	//    rankdir=LR;
	//    newrank=true;
	//    ranksep="1.5";
	//    quantum="0.5";
	//`)
	// Deterministic ordering.
	allNodes := make([]string, 0, len(nodes))
	for n := range nodes {
		allNodes = append(allNodes, n)
	}
	sort.Strings(allNodes)
	indexes := make(map[string]int)
	for i, name := range allNodes {
		indexes[name] = i
	}
	for i, name := range allNodes {
		fmt.Fprintf(out, "    N%d[%q]\n", i, name)
	}

	froms := make([]string, 0, len(edges))
	for f := range edges {
		froms = append(froms, f)
	}
	sort.Strings(froms)

	for _, f := range froms {
		tos := make([]string, 0, len(edges[f]))
		for t := range edges[f] {
			tos = append(tos, t)
		}
		sort.Strings(tos)
		for _, t := range tos {
			fmt.Fprintf(out, "    N%d --> N%d\n", indexes[f], indexes[t])
		}
	}
	nodeColor := func(className, color string, choose func(name string) bool) {
		var selected []string
		for i, name := range allNodes {
			if choose(name) {
				selected = append(selected, fmt.Sprintf("N%d", i))
			}
		}
		if len(selected) == 0 {
			return
		}
		fmt.Fprintf(out, "    classDef %s fill:%s,stroke:#333,stroke-width:1px;\n", className, color)
		fmt.Fprintf(out, "    class %s %s;\n", strings.Join(selected, ","), className)
	}
	nodeColor("mainModule", mainColor, func(name string) bool {
		return name == mainMod
	})
	nodeColor("testOnlyDep", testColor, func(name string) bool {
		_, isTestOnly := testOnly[name]
		return isTestOnly && name != mainMod
	})
	nodeColor("regularDep", nonTestColor, func(name string) bool {
		_, isTestOnly := testOnly[name]
		return !isTestOnly && name != mainMod
	})
	fmt.Fprintf(out, "```\n")
}

func difference(a, b map[string]struct{}) map[string]struct{} {
	res := make(map[string]struct{})
	for k := range a {
		if _, ok := b[k]; !ok {
			res[k] = struct{}{}
		}
	}
	return res
}
