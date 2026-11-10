package adapters

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The small GPU tier runs large-v3-turbo; a profile naming it must pass
// validation, or the job fails before it reaches any host.
func TestWhisperXAcceptsLargeV3Turbo(t *testing.T) {
	adapter := NewWhisperXAdapter(t.TempDir())

	require.NoError(t, adapter.ValidateParameters(map[string]interface{}{"model": "large-v3-turbo"}))
	require.Contains(t, adapter.GetSupportedModels(), "large-v3-turbo")
}

func TestWhisperXRejectsUnknownModel(t *testing.T) {
	adapter := NewWhisperXAdapter(t.TempDir())

	require.Error(t, adapter.ValidateParameters(map[string]interface{}{"model": "large-v9"}))
}
