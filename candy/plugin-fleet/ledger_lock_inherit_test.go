package deploy

import (
	"context"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestLedgerLockInheritance pins the charly#680 fix: a forked nested member teardown
// (spawned by deploykit.TearDownMembers from a top-level `deploy del`) must JOIN its
// ancestor's ledger transaction, not re-acquire the lock its ancestor still holds.
// flock is per-open-file-description, so re-acquiring self-deadlocks — measured live as a
// 30-minute `waiting for file lock … .ledger.lock` stall on a group bed whose root is an
// external-in-place substrate (kindcluster) with a deploy-level member.
func TestLedgerLockInheritance(t *testing.T) {
	// A plain ctx (no marker) → NOT inherited.
	if ledgerLockHeld(context.Background()) {
		t.Fatal("a bare ctx must NOT be considered lock-inherited")
	}
	// The marker ctx → inherited.
	held := withLedgerLockHeld(context.Background())
	if !ledgerLockHeld(held) {
		t.Fatal("a ctx carrying the marker MUST be considered lock-inherited")
	}
	// The process-env fallback (legacy): the child reads the marker from os.Getenv when
	// its ctx carries no RunEnv.
	t.Setenv(ledgerLockHeldEnv, "1")
	if !ledgerLockHeld(context.Background()) {
		t.Fatal("the env marker must be honoured (legacy os.Getenv fallback)")
	}
	t.Setenv(ledgerLockHeldEnv, "")
	if ledgerLockHeld(context.Background()) {
		t.Fatal("an empty env marker must NOT count as held")
	}
}

// TestAcquireLedgerLockForDel_InheritedIsNoop proves the inherited branch takes NO lock:
// it returns a working no-op release and never touches the ledger lock file. Without the
// fix, this branch did not exist and a nested member del would block on the ancestor's
// lock.
func TestAcquireLedgerLockForDel_InheritedIsNoop(t *testing.T) {
	release, err := acquireLedgerLockForDel(withLedgerLockHeld(context.Background()))
	if err != nil {
		t.Fatalf("inherited acquire must succeed without taking the lock: %v", err)
	}
	if release == nil {
		t.Fatal("inherited acquire must return a non-nil release func")
	}
	release() // must be safe to call; must not error
}

// TestWithLedgerLockHeld_ PreservesExistingRunEnv: the marker merge must NOT clobber a
// bed's own RunEnv (CHARLY_DEPLOY_CONFIG / CHARLY_REPO_OVERRIDE) — clobbering it would
// break bed isolation, a regression worse than the deadlock.
func TestWithLedgerLockHeld_PreservesExistingRunEnv(t *testing.T) {
	base := spec.WithRunEnv(context.Background(), spec.RunEnv{
		"CHARLY_DEPLOY_CONFIG": "/tmp/bed.yml",
		"CHARLY_REPO_OVERRIDE": "github.com/x/y=/tmp/y",
	})
	got := withLedgerLockHeld(base)
	env := spec.RunEnvFrom(got)
	if env["CHARLY_DEPLOY_CONFIG"] != "/tmp/bed.yml" {
		t.Fatalf("deploy-config clobbered: %q", env["CHARLY_DEPLOY_CONFIG"])
	}
	if env["CHARLY_REPO_OVERRIDE"] != "github.com/x/y=/tmp/y" {
		t.Fatalf("repo-override clobbered: %q", env["CHARLY_REPO_OVERRIDE"])
	}
	if env[ledgerLockHeldEnv] != "1" {
		t.Fatalf("marker not set: %q", env[ledgerLockHeldEnv])
	}
}
