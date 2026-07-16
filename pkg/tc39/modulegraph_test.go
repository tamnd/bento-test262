package tc39

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestRelativeSpecifiers(t *testing.T) {
	src := `/*---
description: a from '././not-an-import.js' in the description does not count
flags: [module]
---*/
import { x } from './dep_FIXTURE.js';
export { y } from './dep_FIXTURE.js';
export * as ns from './other_FIXTURE.js';
import './side-effect_FIXTURE.js';
import pkg from 'some-package';
import node from 'node:fs';
import self from './self.js';
`
	got := relativeSpecifiers(src)
	want := []string{
		"./dep_FIXTURE.js",
		"./other_FIXTURE.js",
		"./side-effect_FIXTURE.js",
		"./self.js",
	}
	if len(got) != len(want) {
		t.Fatalf("relativeSpecifiers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("specifier %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStagedName(t *testing.T) {
	cases := map[string]string{
		"./dep_FIXTURE.js": "dep_FIXTURE.ts",
		"./sub/dep.js":     "sub/dep.ts",
		"./dep.mjs":        "dep.mts",
		"./no-extension":   "no-extension",
		"./a/../dep.js":    "dep.ts",
	}
	for spec, want := range cases {
		if got := stagedName(spec); got != want {
			t.Errorf("stagedName(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestResolveModuleGraphStagesSiblings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dep_FIXTURE.js", "export const x = 1;\nexport { back } from './entry.js';\n")
	writeFile(t, dir, "entry.js", "import { x } from './dep_FIXTURE.js';\nexport const back = x;\n")

	entryPath := filepath.Join(dir, "entry.js")
	entrySrc, _ := os.ReadFile(entryPath)
	mods, err := resolveModuleGraph(entryPath, string(entrySrc))
	if err != nil {
		t.Fatalf("resolveModuleGraph: %v", err)
	}
	// The one fixture is staged; the entry's re-export back into itself does not
	// stage a second copy of the entry.
	if len(mods) != 1 {
		t.Fatalf("staged %d modules, want 1: %+v", len(mods), mods)
	}
	if mods[0].Name != "dep_FIXTURE.ts" {
		t.Errorf("staged name = %q, want dep_FIXTURE.ts", mods[0].Name)
	}
	if mods[0].Source == "" {
		t.Errorf("staged source is empty")
	}
}

func TestResolveModuleGraphFollowsFixtureChain(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a_FIXTURE.js", "export { v } from './b_FIXTURE.js';\n")
	writeFile(t, dir, "b_FIXTURE.js", "export const v = 2;\n")
	writeFile(t, dir, "entry.js", "import { v } from './a_FIXTURE.js';\nexport const w = v;\n")

	entryPath := filepath.Join(dir, "entry.js")
	entrySrc, _ := os.ReadFile(entryPath)
	mods, err := resolveModuleGraph(entryPath, string(entrySrc))
	if err != nil {
		t.Fatalf("resolveModuleGraph: %v", err)
	}
	var names []string
	for _, m := range mods {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "a_FIXTURE.ts" || names[1] != "b_FIXTURE.ts" {
		t.Fatalf("staged names = %v, want [a_FIXTURE.ts b_FIXTURE.ts]", names)
	}
}

func TestResolveModuleGraphSkipsMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "entry.js", "import { x } from './gone_FIXTURE.js';\nimport pkg from 'left-pad';\n")
	entryPath := filepath.Join(dir, "entry.js")
	entrySrc, _ := os.ReadFile(entryPath)
	mods, err := resolveModuleGraph(entryPath, string(entrySrc))
	if err != nil {
		t.Fatalf("resolveModuleGraph: %v", err)
	}
	if len(mods) != 0 {
		t.Fatalf("staged %d modules for missing/bare imports, want 0", len(mods))
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
