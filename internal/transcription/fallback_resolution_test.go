package transcription

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"scriberr/internal/models"
	"scriberr/internal/repository"
)

type stubProfileRepo struct {
	repository.ProfileRepository
	fallbacks []models.TranscriptionProfile
	err       error
}

func (s *stubProfileRepo) FindFallbacks(context.Context) ([]models.TranscriptionProfile, error) {
	return s.fallbacks, s.err
}

func fallbackProfile(name, language, model string) models.TranscriptionProfile {
	profile := models.TranscriptionProfile{Name: name, IsFallback: true}
	profile.Parameters.Model = model
	profile.Parameters.Device = "cpu"
	if language != "" {
		profile.Parameters.Language = stringPtr(language)
	}
	return profile
}

func serviceWithFallbacks(profiles ...models.TranscriptionProfile) *UnifiedTranscriptionService {
	service := NewUnifiedTranscriptionService(nil, t2Dir(), t2Dir())
	service.SetProfileRepository(&stubProfileRepo{fallbacks: profiles})
	return service
}

func t2Dir() string { return "/tmp" }

func TestResolveFallbackParametersPicksMatchingLanguage(t *testing.T) {
	service := serviceWithFallbacks(
		fallbackProfile("English", "en", "small"),
		fallbackProfile("Spanish", "es", "small"),
	)

	params, _ := service.resolveFallback(context.Background(), stringPtr("es"))

	require.Equal(t, "es", params["language"])
	require.Equal(t, "small", params["model"])
}

func TestResolveFallbackParametersPicksEnglishForEnglishJob(t *testing.T) {
	service := serviceWithFallbacks(
		fallbackProfile("English", "en", "small"),
		fallbackProfile("Spanish", "es", "small"),
	)

	params, _ := service.resolveFallback(context.Background(), stringPtr("en"))

	require.Equal(t, "en", params["language"])
}

func TestResolveFallbackParametersMatchesCaseInsensitively(t *testing.T) {
	service := serviceWithFallbacks(fallbackProfile("Spanish", "ES", "small"))

	params, _ := service.resolveFallback(context.Background(), stringPtr("es"))

	require.Equal(t, "ES", params["language"])
}

// A small model in the right language beats a large one in the wrong language.
func TestResolveFallbackParametersKeepsJobLanguageWhenNoProfileMatches(t *testing.T) {
	service := serviceWithFallbacks(fallbackProfile("Spanish", "es", "small"))

	params, _ := service.resolveFallback(context.Background(), stringPtr("de"))

	require.Equal(t, "de", params["language"])
	require.Equal(t, "small", params["model"])
}

func TestResolveFallbackParametersWithoutJobLanguageUsesFirstProfile(t *testing.T) {
	service := serviceWithFallbacks(
		fallbackProfile("English", "en", "small"),
		fallbackProfile("Spanish", "es", "small"),
	)

	params, _ := service.resolveFallback(context.Background(), nil)

	require.Equal(t, "en", params["language"])
}

func TestResolveFallbackParametersNilWhenNoneFlagged(t *testing.T) {
	service := serviceWithFallbacks()

	params, _ := service.resolveFallback(context.Background(), stringPtr("es"))
	require.Nil(t, params)
}

func TestResolveFallbackParametersNilWithoutProfileRepository(t *testing.T) {
	service := NewUnifiedTranscriptionService(nil, t2Dir(), t2Dir())

	params, _ := service.resolveFallback(context.Background(), stringPtr("es"))
	require.Nil(t, params)
}

func TestResolveFallbackReturnsTheChosenProfilesGPUHost(t *testing.T) {
	spanish := fallbackProfile("Spanish", "es", "large-v3-turbo")
	spanish.ExecutionMode = "remote"
	spanish.RemoteHost = "second-gpu.example"
	spanish.RemoteUser = "scriberr"
	spanish.RemoteWorkDir = "/srv/second"
	service := serviceWithFallbacks(fallbackProfile("English", "en", "small"), spanish)

	params, execution := service.resolveFallback(context.Background(), stringPtr("es"))

	require.Equal(t, "large-v3-turbo", params["model"])
	require.Equal(t, "remote", execution.Mode)
	require.Equal(t, "second-gpu.example", execution.RemoteHost)
	require.Equal(t, "scriberr", execution.RemoteUser)
	require.Equal(t, "/srv/second", execution.RemoteWorkDir)
}
