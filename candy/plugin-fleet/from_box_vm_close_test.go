package deploy

import (
	"errors"
	"strings"
	"testing"
)

// failingCloser is an io.Closer whose Close reports a short or lost write.
type failingCloser struct{ err error }

func (c failingCloser) Close() error { return c.err }

// closeInto is the ONE place extractVmBoxDisk turns a close failure into a return: a failed close
// with no earlier error becomes that error (named with the destination), a failed close after an
// earlier error leaves the earlier error standing, and a clean close changes nothing.
func TestCloseIntoReportsAFailedClose(t *testing.T) {
	short := errors.New("short write")

	var none error
	closeInto(&none, failingCloser{err: short}, "/tmp/disks/demo.qcow2")
	if none == nil || !errors.Is(none, short) || !strings.Contains(none.Error(), "/tmp/disks/demo.qcow2") {
		t.Errorf("a failed close was not reported against its destination: %v", none)
	}

	earlier := errors.New("tar failed")
	pending := earlier
	closeInto(&pending, failingCloser{err: short}, "/tmp/disks/demo.qcow2")
	if !errors.Is(pending, earlier) {
		t.Errorf("a close failure masked the earlier error: %v", pending)
	}

	var clean error
	closeInto(&clean, failingCloser{}, "/tmp/disks/demo.qcow2")
	if clean != nil {
		t.Errorf("a clean close produced an error: %v", clean)
	}
}
