package alloc

import (
	"net"
	"testing"
)

// listen opens a listener on an OS-chosen port outside the devrun range.
func listen(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}
