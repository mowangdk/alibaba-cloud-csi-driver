package proxy

import (
	"time"
)

// Every duration the mount protocol reasons about lives here, because getting a
// mount error back to whoever asked for it depends on how they line up.
//
// A mount crosses four boundaries, each with its own idea of when to stop:
//
//	sandbox / kubelet ──gRPC, its own deadline──► csi-plugin
//	  earlyTimeout (pkg/common) hands the handler a deadline slightly earlier,
//	  keeping a moment to write the gRPC response after the handler returns
//	                              │
//	                              ▼
//	csi-plugin ──unix socket, Header.DeadlineUnixNano──► mount-proxy-server
//	  the client states when it stops waiting: the caller's deadline, or
//	  ClientFallbackTimeout when the caller set none
//	                              │
//	                              ▼
//	mount-proxy-server ──context──► ossfs / ossfs2 / alinas
//	  ResolveWorkDeadline takes DeadlineReserve off what the client stated, so
//	  the mount stops with time left to wind down and answer
//	                              │
//	                              ▼
//	  the connection stays writable ResponseWriteWindow past its own timeout,
//	  so an answer produced at the very end still goes out
//
// The client states an absolute instant rather than a duration, which is only
// meaningful because both ends share a kernel. Deriving the server's budget
// from that instant, instead of comparing two independently configured
// timeouts, is what keeps the connection timeout free to be tuned: whatever
// --timeout is set to, the work still stops before the client stops waiting.
const (
	// DefaultConnectionTimeout is how long mount-proxy-server gives a single
	// connection to deliver a request, and the base for how long it may take to
	// answer. It bounds a mount only as a ceiling, and only when it is shorter
	// than what the client stated.
	DefaultConnectionTimeout = 30 * time.Second

	// ResponseWriteWindow is how much longer than the work itself the connection
	// stays writable, so an answer produced right at the deadline still fits. It
	// has to exceed DeadlineReserve, which is how late an answer can be.
	ResponseWriteWindow = 5 * time.Second

	// MountShutdownGrace is how long the FUSE drivers wait between SIGTERM and
	// SIGKILL, and therefore how long a mount keeps running after its context
	// fires.
	//
	// This is specific to ossfs and ossfs2, and is the main way they differ from
	// alinas: a FUSE mount is a child process this daemon owns and has to reap,
	// while alinas hands the work to mount-utils and cannot interrupt it at all.
	// The drivers read the value from here rather than spelling it out, so the
	// reserve below and the wait they actually perform cannot drift apart.
	MountShutdownGrace = 2 * time.Second

	// DeadlineReserve is how much of the client's remaining time is left unused,
	// so that a timed-out mount still reports why.
	//
	// It covers more than the shutdown: the instant the client stated is also
	// when it closes the connection, so an answer merely ready by then races
	// that close. The extra second is for the answer to cross a local socket,
	// not for any real latency. What earlyTimeout holds back does not help here,
	// as that belongs to the gRPC layer above.
	DeadlineReserve = MountShutdownGrace + time.Second

	// ClientFallbackTimeout is how long the client states it will wait when the
	// caller gave no deadline of its own.
	//
	// Only bmcpfs reaches this in production: its NodePublishVolume has a
	// context but calls the mounter through the ctx-less mount.Interface, so the
	// deadline is lost on the way down. The debug CLI has no context to begin
	// with.
	//
	// TODO: pass the context through in bmcpfs, and this becomes unreachable
	// outside the CLI.
	ClientFallbackTimeout = 35 * time.Second
)

// ResolveWorkDeadline decides when a mount has to give up.
//
// stated is the instant the client said it would stop waiting, zero for clients
// that predate Header.DeadlineUnixNano. connDeadline is when the connection
// itself expires, and caps the result: work outliving its connection would
// answer into a socket nobody reads.
func ResolveWorkDeadline(now, stated, connDeadline time.Time) time.Time {
	if stated.IsZero() {
		return connDeadline
	}
	if stated.Sub(now) <= DeadlineReserve {
		// Reporting is the point, so when there is not enough time for both,
		// spend none of it mounting rather than shorten the reserve.
		return now
	}
	if deadline := stated.Add(-DeadlineReserve); deadline.Before(connDeadline) {
		return deadline
	}
	return connDeadline
}
