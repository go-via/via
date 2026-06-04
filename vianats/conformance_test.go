package vianats_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/backplanetest"
	"github.com/go-via/vianats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
	"github.com/stretchr/testify/require"
)

// The NATS backend must satisfy the SAME Backplane contract as the in-memory
// reference — this is the RELEASE-GATING real-backend conformance run: a green
// in-mem suite never stands in for a real ordered, durable, resumable log.
// Each conformance subtest gets a freshly-named bucket+stream (unique prefix) on
// one embedded JetStream server, so subtests are isolated.
func TestJetStreamConformance(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)

	backplanetest.RunConformance(t, func() via.Backplane {
		nc, err := nats.Connect(url)
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		bp, err := vianats.JetStream(nc, vianats.WithPrefix("t"+nuid.Next()))
		require.NoError(t, err)
		return bp
	})
}
