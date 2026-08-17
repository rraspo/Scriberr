package adapters

import (
	"testing"

	"github.com/stretchr/testify/require"

	"scriberr/internal/transcription/interfaces"
)

// A GPU profile's model and batch size are sized for a 4090. Falling back to the
// container's cores with those values is what the is_fallback profile prevents.
func TestFallbackParametersPrefersFallbackProfile(t *testing.T) {
	jobParams := map[string]interface{}{
		"model": "large-v3", "device": "cuda", "batch_size": 24, "compute_type": "float16",
	}
	procCtx := interfaces.ProcessingContext{
		FallbackParameters: map[string]interface{}{
			"model": "small", "device": "cpu", "batch_size": 8, "language": "es",
		},
	}

	result := fallbackParameters(procCtx, jobParams)

	require.Equal(t, "small", result["model"])
	require.Equal(t, 8, result["batch_size"])
	require.Equal(t, "es", result["language"])
	require.Equal(t, "cpu", result["device"])
	require.Equal(t, "float32", result["compute_type"])
}

func TestFallbackParametersForcesCPUEvenIfFallbackProfileSaysCUDA(t *testing.T) {
	procCtx := interfaces.ProcessingContext{
		FallbackParameters: map[string]interface{}{"model": "small", "device": "cuda"},
	}

	result := fallbackParameters(procCtx, map[string]interface{}{"model": "large-v3"})

	require.Equal(t, "cpu", result["device"])
}

func TestFallbackParametersKeepsJobParamsWhenNoFallbackProfile(t *testing.T) {
	jobParams := map[string]interface{}{"model": "large-v3", "device": "cuda", "batch_size": 24}

	result := fallbackParameters(interfaces.ProcessingContext{}, jobParams)

	require.Equal(t, "large-v3", result["model"])
	require.Equal(t, "cpu", result["device"])
	require.Equal(t, "float32", result["compute_type"])
}

func TestFallbackParametersCarriesHfTokenWhenFallbackProfileLacksOne(t *testing.T) {
	procCtx := interfaces.ProcessingContext{
		FallbackParameters: map[string]interface{}{"model": "small", "diarize": true},
	}

	result := fallbackParameters(procCtx, map[string]interface{}{"hf_token": "hf_fromjob"})

	require.Equal(t, "hf_fromjob", result["hf_token"])
}

func TestFallbackParametersDoesNotMutateContext(t *testing.T) {
	procCtx := interfaces.ProcessingContext{
		FallbackParameters: map[string]interface{}{"model": "small", "device": "cuda"},
	}

	fallbackParameters(procCtx, map[string]interface{}{})

	// The context is reused across retries; the stored profile must stay intact.
	require.Equal(t, "cuda", procCtx.FallbackParameters["device"])
}
