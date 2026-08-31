package proxy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestResolveWorkDeadline(t *testing.T) {
	tests := []struct {
		name       string
		stated     time.Duration
		noStated   bool
		connOffset time.Duration
		expect     time.Duration
	}{
		{
			name:       "a client that states nothing leaves the connection in charge",
			noStated:   true,
			connOffset: DefaultConnectionTimeout,
			expect:     DefaultConnectionTimeout,
		},
		{
			name:       "the stated instant governs, less the reserve",
			stated:     20 * time.Second,
			connOffset: DefaultConnectionTimeout,
			expect:     20*time.Second - DeadlineReserve,
		},
		{
			name:       "a connection expiring first becomes the ceiling",
			stated:     10 * time.Minute,
			connOffset: DefaultConnectionTimeout,
			expect:     DefaultConnectionTimeout,
		},
		{
			name:       "a connection timeout raised past what the client stated changes nothing",
			stated:     35 * time.Second,
			connOffset: 60 * time.Second,
			expect:     35*time.Second - DeadlineReserve,
		},
		{
			name:       "a connection timeout below the reserve still yields its own instant",
			stated:     35 * time.Second,
			connOffset: 2 * time.Second,
			expect:     2 * time.Second,
		},
		{
			name:       "no time for both mounting and reporting means no mounting",
			stated:     DeadlineReserve,
			connOffset: DefaultConnectionTimeout,
			expect:     0,
		},
		{
			name:       "an instant already past yields no time either",
			stated:     -time.Second,
			connOffset: DefaultConnectionTimeout,
			expect:     0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stated time.Time
			if !tt.noStated {
				stated = now.Add(tt.stated)
			}
			got := ResolveWorkDeadline(now, stated, now.Add(tt.connOffset))
			assert.Equal(t, now.Add(tt.expect), got)
		})
	}
}

// Whatever the connection timeout is set to, a mount bounded by what the client
// stated has to finish winding down before the client stops waiting. This is the
// invariant that replaced comparing the two timeouts against each other, so it
// is checked across the range an operator could plausibly configure.
func TestWorkAlwaysEndsBeforeClientStopsWaiting(t *testing.T) {
	for _, connTimeout := range []time.Duration{
		5 * time.Second,
		DefaultConnectionTimeout,
		40 * time.Second,
		2 * time.Minute,
	} {
		for _, stated := range []time.Duration{
			5 * time.Second,
			20 * time.Second,
			ClientFallbackTimeout,
			2 * time.Minute,
		} {
			statedAt := now.Add(stated)
			work := ResolveWorkDeadline(now, statedAt, now.Add(connTimeout))
			answeredBy := work.Add(MountShutdownGrace)

			if work.After(now.Add(connTimeout)) {
				t.Fatalf("conn=%v stated=%v: work outlives its connection", connTimeout, stated)
			}
			// A connection shorter than the client's patience ends the work
			// early, and the write window covers answering after that.
			if work.Equal(now.Add(connTimeout)) {
				continue
			}
			assert.True(t, answeredBy.Before(statedAt),
				"conn=%v stated=%v: answer ready at %v, client already gone at %v",
				connTimeout, stated, answeredBy.Sub(now), stated)
		}
	}
}

// The window has to outlast the latest an answer can be, or work that ran to the
// end of its connection could never report.
func TestResponseWriteWindowOutlastsLateAnswer(t *testing.T) {
	assert.Greater(t, ResponseWriteWindow, DeadlineReserve)
	assert.Greater(t, DeadlineReserve, MountShutdownGrace)
}

// A client that states nothing must serialise to the bytes an older server
// already understands, since the two are released independently.
func TestHeaderOmitsUnstatedDeadline(t *testing.T) {
	data, err := json.Marshal(Request{Header: Header{Method: Mount}})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "deadlineUnixNano")
}

// And a server that predates the field has to survive being sent one.
func TestOlderServerIgnoresStatedDeadline(t *testing.T) {
	withField, err := json.Marshal(Request{Header: Header{
		Method:           Mount,
		DeadlineUnixNano: now.UnixNano(),
	}})
	require.NoError(t, err)

	type headerBeforeTheField struct {
		Method Method `json:"method,omitempty"`
	}
	var old struct {
		Header headerBeforeTheField `json:"header"`
	}
	require.NoError(t, json.Unmarshal(withField, &old))
	assert.Equal(t, Mount, old.Header.Method)
}
