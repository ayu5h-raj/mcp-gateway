package supervisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestState_StringAndValidTransitions(t *testing.T) {
	assert.Equal(t, "starting", StateStarting.String())
	assert.Equal(t, "running", StateRunning.String())
	assert.Equal(t, "errored", StateErrored.String())
	assert.Equal(t, "restarting", StateRestarting.String())
	assert.Equal(t, "disabled", StateDisabled.String())
	assert.Equal(t, "stopped", StateStopped.String())
}

func TestBackoff_Schedule(t *testing.T) {
	b := NewBackoff(60)
	// Sequence: 1s, 2s, 4s, 8s, 16s, 32s, 60s (capped), 60s, ...
	expectSecs := []int{1, 2, 4, 8, 16, 32, 60, 60}
	for i, want := range expectSecs {
		got := b.Next().Seconds()
		assert.InDelta(t, want, got, 0.001, "attempt %d", i)
	}
}

func TestBackoff_ResetsOnSuccess(t *testing.T) {
	b := NewBackoff(60)
	_ = b.Next() // 1s
	_ = b.Next() // 2s
	b.Reset()
	assert.InDelta(t, 1.0, b.Next().Seconds(), 0.001)
}
