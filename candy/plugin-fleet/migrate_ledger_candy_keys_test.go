package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// migrate_ledger_candy_keys_test.go — relocated from charly/migrate_ledger_candy_keys_test.go (#55
// decoupling, Batch A): zero charly coupling.

// TestReadCandyRecord_GatesPreCutover proves the ledger read path hard-rejects a
// pre-cutover record (no schema_version) with an actionable error.
func TestReadCandyRecord_GatesPreCutover(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "charly.yml")
	// A pre-cutover ledger record: a candy entry with no schema_version.
	if err := os.WriteFile(cfg, []byte("version: 2026.240.1943\nledger:\n    candies:\n        old:\n            candy: old\n            deployed_by: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := kit.ReadCandyRecord(&kit.LedgerPaths{ConfigFile: cfg, LockFile: cfg + ".lock"}, "old")
	if err == nil {
		t.Fatal("expected gate error on a pre-cutover record")
	}
	if !strings.Contains(err.Error(), "pre-cutover install-ledger record") {
		t.Errorf("gate error should explain the pre-cutover record: %v", err)
	}
}
