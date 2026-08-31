package client

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingServer accepts one request, hands its header back and answers, which
// is what makes the client's own deadline arithmetic observable: none of it
// shows up anywhere else without a real mount.
func recordingServer(t *testing.T) (socketPath string, headers <-chan proxy.Header) {
	t.Helper()

	// Not t.TempDir(): its name carries the test's, and a unix socket path is
	// capped at 104 bytes on darwin, which long test names overrun.
	dir, err := os.MkdirTemp("", "px")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath = filepath.Join(dir, "s.sock")
	addr := net.UnixAddr{Name: socketPath, Net: "unix"}
	listener, err := net.ListenUnix("unix", &addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	got := make(chan proxy.Header, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		var req struct {
			Header proxy.Header    `json:"header"`
			Body   json.RawMessage `json:"body,omitempty"`
		}
		if err := proxy.ReadMsg(conn, &req); err != nil {
			return
		}
		got <- req.Header

		data, _ := json.Marshal(proxy.Response{})
		_, _ = conn.Write(append(data, proxy.MessageEnd))
	}()

	return socketPath, got
}

func awaitHeader(t *testing.T, headers <-chan proxy.Header) proxy.Header {
	t.Helper()
	select {
	case h := <-headers:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("server never saw a request")
		return proxy.Header{}
	}
}

func TestStatesCallerDeadline(t *testing.T) {
	socketPath, headers := recordingServer(t)

	callerDeadline := time.Now().Add(20 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()

	_, err := NewClient(socketPath).Mount(ctx, &proxy.MountRequest{Target: "/tmp/x"})
	require.NoError(t, err)

	h := awaitHeader(t, headers)
	require.NotZero(t, h.DeadlineUnixNano)
	assert.Equal(t, callerDeadline.UnixNano(), h.DeadlineUnixNano,
		"the caller's own instant must be passed through untouched")
}

// A caller without a deadline still has to produce one, or the server would fall
// back to its connection timeout and the whole derivation would be skipped.
func TestStatesFallbackWhenCallerHasNoDeadline(t *testing.T) {
	socketPath, headers := recordingServer(t)

	before := time.Now()
	_, err := NewClient(socketPath).Mount(context.Background(), &proxy.MountRequest{Target: "/tmp/x"})
	require.NoError(t, err)

	h := awaitHeader(t, headers)
	require.NotZero(t, h.DeadlineUnixNano)

	stated := time.Unix(0, h.DeadlineUnixNano)
	assert.WithinDuration(t, before.Add(proxy.ClientFallbackTimeout), stated, time.Second)
}

// A caller in less of a hurry than the fallback keeps its own instant: taking the
// shorter of the two is what lets the server trust what it is told.
func TestStatesTheEarlierOfCallerAndFallback(t *testing.T) {
	socketPath, headers := recordingServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), proxy.ClientFallbackTimeout+time.Minute)
	defer cancel()

	before := time.Now()
	_, err := NewClient(socketPath).Mount(ctx, &proxy.MountRequest{Target: "/tmp/x"})
	require.NoError(t, err)

	h := awaitHeader(t, headers)
	stated := time.Unix(0, h.DeadlineUnixNano)
	assert.WithinDuration(t, before.Add(proxy.ClientFallbackTimeout), stated, time.Second)
}
