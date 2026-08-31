// Command catalog-gate enforces the connector catalog's narrow-only
// rule mechanically: an edit to an existing operation may only narrow or
// preserve its reach — anything wider must ship as a new op name.
//
//	catalog-gate                     check embedded defs against the embedded baseline
//	                                 (the same check nightshift serve runs at startup)
//	catalog-gate OLD_DIR NEW_DIR     diff two catalog definition directories; exits 1
//	                                 on any reach-widening change (CI: OLD_DIR from the
//	                                 merge base, NEW_DIR from the candidate)
//	catalog-gate -update-baseline    copy defs/ over baseline/ after a reviewed,
//	                                 non-widening catalog edit
//
// The repo has no CI yet; until it does, the startup baseline check is
// the enforcement floor and the two-dir mode is what a future CI job
// runs against the merge base.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gambtho/nightwatch/server/internal/catalog"
)

func main() {
	update := flag.Bool("update-baseline", false, "copy defs/ over baseline/ in -catalog-dir")
	dir := flag.String("catalog-dir", "internal/catalog", "catalog package directory (for -update-baseline)")
	flag.Parse()

	var err error
	switch {
	case *update:
		err = updateBaseline(*dir)
	case flag.NArg() == 2:
		err = diffDirs(flag.Arg(0), flag.Arg(1))
	case flag.NArg() == 0:
		_, err = catalog.Load()
		if err == nil {
			fmt.Println("catalog-gate: embedded defs match baseline, no widening")
		}
	default:
		err = fmt.Errorf("usage: catalog-gate [-update-baseline] [OLD_DIR NEW_DIR]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalog-gate:", err)
		os.Exit(1)
	}
}

func loadDir(dir string) (*catalog.Catalog, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no *.json definitions in %s", dir)
	}
	var defs [][]byte
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		defs = append(defs, raw)
	}
	return catalog.ParseDefs(defs...)
}

func diffDirs(oldDir, newDir string) error {
	oldCat, err := loadDir(oldDir)
	if err != nil {
		return fmt.Errorf("old catalog: %w", err)
	}
	newCat, err := loadDir(newDir)
	if err != nil {
		return fmt.Errorf("new catalog: %w", err)
	}
	if ws := catalog.Widenings(oldCat, newCat); len(ws) > 0 {
		for _, w := range ws {
			fmt.Fprintln(os.Stderr, "WIDENING:", w)
		}
		return fmt.Errorf("%d reach-widening change(s); ship wider reach as a NEW op name", len(ws))
	}
	fmt.Println("catalog-gate: no reach-widening changes")
	return nil
}

func updateBaseline(dir string) error {
	defs, baseline := filepath.Join(dir, "defs"), filepath.Join(dir, "baseline")
	// Validate before copying: a broken defs/ must not become the baseline.
	if _, err := loadDir(defs); err != nil {
		return err
	}
	old, err := filepath.Glob(filepath.Join(baseline, "*.json"))
	if err != nil {
		return err
	}
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	files, err := filepath.Glob(filepath.Join(defs, "*.json"))
	if err != nil {
		return err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(baseline, filepath.Base(f)), raw, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("catalog-gate: baseline updated from %d definition(s)\n", len(files))
	return nil
}
