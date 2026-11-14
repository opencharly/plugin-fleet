package deploy

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// config_cmd.go — the K4-C move of the `charly deploy` CONFIG-MANAGEMENT subcommands
// (show/export/import/reset/status) out of charly core. Every handler below calls ONLY
// already-sdk-portable deploykit/kit functions. The reads/writes reach the host ONLY for what a
// separate module genuinely cannot hold: InvokeProvider("build","project") for export's
// project-load (the SAME seam compile.go already uses), the loaderkit.LoadHostDeployConfigViaExecutor
// overlay read (loadDeployConfig), and the "loader-threaded" Primaries snapshot. import/reset's deploy-state
// WRITE now runs PLUGIN-SIDE — deploykit.SaveDeployConfig with the plugin's OWN loader-backed
// reader + a marshal callback that resugars each plan step from the loader-threaded Primaries
// (deployMarshalNode), NOT the deleted host "deploy-config-save" seam (#55 K4 config-write
// seam-collapse). IMPORT-PURITY: imports ONLY github.com/opencharly/sdk (deploykit/kit/spec are
// subpackages); never charly/.
//
// Bed-robustness batch item 5 (the placement-dependent silent-no-op class): every READ below
// goes through the package-local loadDeployConfig() (ephemeral.go), which resolves the per-host
// overlay via the cycle-free loaderkit.LoadHostDeployConfigViaExecutor read — NEVER the raw deploykit.LoadDeployConfig()
// (which no-ops errorlessly unless the calling process happens to have registered
// deploykit.DeployStateHost at init — true ONLY while command:deploy stays compiled-in, a
// per-BUILD placement fact, never an authoring guarantee). This was DORMANT (not an active bug)
// because plugin-fleet is compiled-in TODAY, but every one of these 6 call sites would have
// silently degraded to "no charly.yml configured" the moment plugin-fleet is ever built
// out-of-process — exactly the failure mode plugin-deploy-vm's vmPrepareVenue hit for real
// (lifecycle.go, same batch). Fixed prophylactically here rather than left for the next
// placement change to rediscover.

// fetchResolvedProject moved to compile.go (R3 — the SINGLE resolved-project envelope fetch, shared
// by the config leg, the per-shape compile, and the walk's ref classification). The 3-arg form takes
// (dir, extraCandyRefs, includeDisabled); this config caller passes (dir, nil, false).

// deployMarshalNode builds the per-entry node-form marshal callback deploykit.SaveDeployConfig /
// SaveDeployState take. It resugars each plan step via the loader-threaded Primaries snapshot
// (fetchLoaderPrimaries) — the SAME registry-derived D-fact the deleted host deploy-config-save
// leg fed to deploykit.MarshalDeployNode via loaderThreaded().Primaries. Sourcing Primaries
// PLUGIN-SIDE is what lets the deploy-state WRITE run here instead of over a host seam (#55 K4).
func deployMarshalNode() func(name string, node *deploykit.DeployNode) (*yaml.Node, error) {
	primaries := fetchLoaderPrimaries()
	return func(_ string, node *deploykit.DeployNode) (*yaml.Node, error) {
		return deploykit.MarshalDeployNode(node, primaries)
	}
}

// saveDeployConfig persists dc PLUGIN-SIDE via deploykit.SaveDeployConfig directly (#55 K4
// config-write seam-collapse — the narrow HostBuild("deploy-config-save") host leg is deleted).
// loadDeployConfig is the plugin's own loader-backed reader for the write path's fail-safe
// re-check, so the write no longer depends on the host's DeployStateHost registration.
func saveDeployConfig(dc *deploykit.DeployConfig) error {
	return deploykit.SaveDeployConfig(dc, deployMarshalNode(), loadDeployConfig, cmdCtx)
}

// mutateDeployConfig runs one locked read-modify-write cycle over the per-host deploy overlay,
// supplying this plugin's reader + persist callback to the SHARED deploykit.MutateDeployConfig
// cycle. Every write in this plugin goes through it: the mutation runs against a config re-read
// INSIDE the lock, so a concurrent `charly config` / `charly vm create` / bed runner write is
// merged onto rather than clobbered.
//
// It replaces this package's former private lock helper — one of three identical per-candy copies,
// and the one that guarded only the vm-entry removal while `charly deploy import`, `charly deploy
// reset` and the three ephemeral writers below took no lock at all.
func mutateDeployConfig(mutate deploykit.DeployConfigMutator) error {
	_, err := deploykit.MutateDeployConfig(loadDeployConfig, saveDeployConfig, mutate, cmdCtx)
	return err
}

func marshalConfigToStdout(dc *deploykit.DeployConfig) error {
	data, err := yaml.Marshal(dc)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func filterDeployBox(dc *deploykit.DeployConfig, names []string) *deploykit.DeployConfig {
	filtered := &deploykit.DeployConfig{Deploy: make(map[string]spec.DeployNode)}
	for _, name := range names {
		if entry, ok := dc.Deploy[name]; ok {
			filtered.Deploy[name] = entry
		}
	}
	return filtered
}

// runFleetShow serves `charly deploy show [box]`.
func runFleetShow(box, instance string) error {
	dc, err := loadDeployConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Deploy) == 0 {
		fmt.Println("No charly.yml configured")
		return nil
	}
	if box != "" {
		key := spec.DeployKey(box, instance)
		entry, ok := dc.Deploy[key]
		if !ok {
			fmt.Printf("No overrides for box %q\n", key)
			return nil
		}
		out := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{key: entry}}
		return marshalConfigToStdout(out)
	}
	return marshalConfigToStdout(dc)
}

// runFleetExport serves `charly deploy export [boxes...]`.
func runFleetExport(boxes []string, output string, all bool) error {
	var dc *deploykit.DeployConfig
	if all {
		dir, _ := os.Getwd()
		rp, err := fetchResolvedProject(dir, nil, false)
		if err != nil {
			return fmt.Errorf("loading charly.yml: %w", err)
		}
		dc = deploykit.ExportAllBox(rp)
	} else {
		loaded, err := loadDeployConfig()
		if err != nil {
			return err
		}
		if loaded == nil || len(loaded.Deploy) == 0 {
			fmt.Fprintln(os.Stderr, "No charly.yml overrides to export")
			return nil
		}
		dc = loaded
	}
	if len(boxes) > 0 {
		dc = filterDeployBox(dc, boxes)
	}
	if output != "" {
		data, err := yaml.Marshal(dc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(output, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", output, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", output)
		return nil
	}
	return marshalConfigToStdout(dc)
}

// runFleetImport serves `charly deploy import <files...>`.
func runFleetImport(files []string, replace bool, box string) error {
	var inputs []*deploykit.DeployConfig
	for _, f := range files {
		dc, err := deploykit.LoadDeployFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, dc)
	}

	// The merge runs INSIDE the locked cycle against a freshly-read overlay (dc), so an import
	// racing a concurrent `charly config` merges onto that writer's entries instead of discarding
	// them. `--replace` still means wholesale replacement: it merges the input files onto an EMPTY
	// base rather than onto dc.
	if err := mutateDeployConfig(func(dc *deploykit.DeployConfig) (bool, error) {
		base := dc
		if replace {
			base = &deploykit.DeployConfig{Deploy: make(map[string]spec.DeployNode)}
		}
		merged := deploykit.MergeDeployConfigs(append([]*deploykit.DeployConfig{base}, inputs...)...)
		if box != "" {
			// The box must be present in the INPUT files, not merely in the merged result: a box that
			// only the EXISTING config carries would otherwise let `--box` print "Imported 1 entry"
			// over an import that landed nothing for it — the same false success this change exists
			// to remove (review round 1, finding 2).
			// ONE existence check, on the inputs: `merged` is MergeDeployConfigs(base, inputs…), so a
			// second `merged.Deploy[box]` test here would be unreachable — the guard above proves the
			// key is present (review round 3, block 3).
			if countBoxInInputs(inputs, box) == 0 {
				return false, fmt.Errorf("box %q not found in input files", box)
			}
			entry := merged.Deploy[box]
			if replace {
				merged = &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{box: entry}}
			} else {
				// Single-box import: only that entry changes; every other fresh entry stays.
				dc.Deploy[box] = entry
				merged = dc
			}
		}
		*dc = *merged
		return true, nil
	}); err != nil {
		return err
	}

	path, _ := kit.DefaultDeployConfigPath()
	// Count ENTRIES, not files: the file count is a coincidence of how the operator grouped them,
	// and reporting it as the import's result made "Imported 1 file(s)" the success line over an
	// import that landed nothing (sdk#517, the loader half; this is the message half).
	entries := countDeployEntries(inputs)
	if box != "" {
		// countBoxInInputs already proved the box is present in the inputs, so exactly one entry was
		// imported for it.
		entries = countBoxInInputs(inputs, box)
	}
	fmt.Fprintf(os.Stderr, "%s into %s\n", importSuccessLine(entries, len(files)), path)
	return nil
}

// runFleetReset serves `charly deploy reset [box]`.
func runFleetReset(box, instance string) error {
	if box == "" {
		path, err := kit.DefaultDeployConfigPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No charly.yml to remove")
				return nil
			}
			return err
		}
		fmt.Println("Removed charly.yml")
		return nil
	}

	key := spec.DeployKey(box, instance)
	found, emptied := false, false
	// Locked cycle: the entry is looked up and removed in the SAME hold as the write, so a reset
	// racing a concurrent overlay writer can neither miss a just-written entry nor resurrect one.
	// The emptied branch removes the file outright, so it reports changed=false — nothing to save.
	if err := mutateDeployConfig(func(dc *deploykit.DeployConfig) (bool, error) {
		if _, ok := dc.Deploy[key]; !ok {
			return false, nil
		}
		found = true
		deploykit.RemoveBoxDeploy(dc, key)
		if len(dc.Deploy) == 0 {
			path, _ := kit.DefaultDeployConfigPath()
			_ = os.Remove(path)
			emptied = true
			return false, nil
		}
		return true, nil
	}); err != nil {
		return err
	}
	switch {
	case !found:
		fmt.Printf("No overrides for box %q\n", key)
	case emptied:
		fmt.Printf("Removed overrides for %q (charly.yml now empty, removed)\n", key)
	default:
		fmt.Printf("Removed overrides for %q\n", key)
	}
	return nil
}

// runFleetStatus serves `charly deploy status`.
func runFleetStatus() error {
	dc, err := loadDeployConfig()
	if err != nil {
		return err
	}

	qdir, qdirErr := kit.QuadletDir()
	quadletBoxes := make(map[string]bool)
	if qdirErr == nil {
		entries, readErr := os.ReadDir(qdir)
		if readErr == nil {
			for _, e := range entries {
				name := e.Name()
				if strings.HasPrefix(name, "charly-") && strings.HasSuffix(name, ".container") {
					boxName := strings.TrimSuffix(strings.TrimPrefix(name, "charly-"), ".container")
					if boxName != "" {
						quadletBoxes[boxName] = true
					}
				}
			}
		}
	}

	deployToStem := make(map[string]string)
	stemToDeploy := make(map[string]string)
	if dc != nil {
		for key := range dc.Deploy {
			img, inst := spec.ParseDeployKey(key)
			stem := strings.TrimPrefix(kit.ContainerNameInstance(img, inst), "charly-")
			deployToStem[key] = stem
			stemToDeploy[stem] = key
		}
	}

	if len(deployToStem) == 0 && len(quadletBoxes) == 0 {
		fmt.Println("No charly.yml entries and no quadlet files found")
		return nil
	}

	for key, stem := range deployToStem {
		if !quadletBoxes[stem] {
			fmt.Printf("%-40s charly.yml: yes  quadlet: no   (stale config)\n", key)
		}
	}
	for stem := range quadletBoxes {
		if key, ok := stemToDeploy[stem]; ok {
			fmt.Printf("%-40s charly.yml: yes  quadlet: yes  (ok)\n", key)
		} else {
			fmt.Printf("%-40s charly.yml: no   quadlet: yes  (no overrides)\n", stem)
		}
	}

	return nil
}

// importSuccessLine is the import's success sentence WITHOUT its target: the exact text a user reads,
// extracted so a regression of the format (a revert to "%d file(s)", or a wrong field order) fails a
// test instead of shipping — the live proof exercises the line, but coverage is what keeps it.
func importSuccessLine(entries, files int) string {
	return fmt.Sprintf("Imported %d %s from %d file(s)",
		entries, pluralWord(entries, "entry", "entries"), files)
}

// countDeployEntries is the number of deploy entries an import actually carries — the number the
// success line reports, so it can never claim an import of something the files did not contain.
func countDeployEntries(inputs []*deploykit.DeployConfig) int {
	n := 0
	for _, dc := range inputs {
		if dc != nil {
			n += len(dc.Deploy)
		}
	}
	return n
}

// countBoxInInputs reports whether the input files carry the named box (1) or not (0). The import's
// success line and its missing-box error BOTH read this, so the line can never claim an entry the
// files did not contain.
func countBoxInInputs(inputs []*deploykit.DeployConfig, box string) int {
	for _, dc := range inputs {
		if dc == nil {
			continue
		}
		if _, ok := dc.Deploy[box]; ok {
			return 1
		}
	}
	return 0
}

// pluralWord picks the singular or plural word for n. Local to this message: the plugin prints
// one counted line and should not grow a shared helper for it.
func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
