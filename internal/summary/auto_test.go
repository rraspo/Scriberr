package summary

import (
	"context"
	"errors"
	"testing"
	"time"

	"scriberr/internal/llm"
	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

type fakeTemplates struct {
	templates []models.SummaryTemplate
	saved     []models.Summary
}

func (f *fakeTemplates) List(context.Context, int, int) ([]models.SummaryTemplate, int64, error) {
	return f.templates, int64(len(f.templates)), nil
}

func (f *fakeTemplates) SaveSummary(_ context.Context, summary *models.Summary) error {
	f.saved = append(f.saved, *summary)
	return nil
}

type fakeJobs struct {
	job          models.TranscriptionJob
	cachedSumary string
}

func (f *fakeJobs) FindByID(context.Context, interface{}) (*models.TranscriptionJob, error) {
	return &f.job, nil
}

func (f *fakeJobs) UpdateSummary(_ context.Context, _ string, summary string) error {
	f.cachedSumary = summary
	return nil
}

type fakeSpeakers struct{ mappings []models.SpeakerMapping }

func (f *fakeSpeakers) ListByJob(context.Context, string) ([]models.SpeakerMapping, error) {
	return f.mappings, nil
}

type fakeLLM struct {
	llm.Service
	messages []llm.ChatMessage
	err      error
}

func (f *fakeLLM) ChatCompletion(_ context.Context, model string, messages []llm.ChatMessage, _ float64) (*llm.ChatResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.messages = messages
	response := &llm.ChatResponse{Model: "served-model"}
	response.Choices = make([]struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}, 1)
	response.Choices[0].Message.Content = "## Resumen\nTodo bien."
	return response, nil
}

func transcriptJSON() *string {
	value := `{"text":"Hola. Adiós.","segments":[{"start":0,"end":1,"text":" Hola.","speaker":"SPEAKER_00"},{"start":1,"end":2,"text":" Adiós.","speaker":"SPEAKER_01"}]}`
	return &value
}

func newSummarizer(templates *fakeTemplates, jobs *fakeJobs, service *fakeLLM) *AutoSummarizer {
	speakers := &fakeSpeakers{mappings: []models.SpeakerMapping{{OriginalSpeaker: "SPEAKER_00", CustomName: "Ana"}}}
	return NewAutoSummarizer(templates, jobs, speakers, func(context.Context) (llm.Service, error) { return service, nil })
}

func TestCompletedJobGetsTheFlaggedTemplatesSummary(t *testing.T) {
	templates := &fakeTemplates{templates: []models.SummaryTemplate{
		{ID: "manual", Name: "Manual", Prompt: "no", CreatedAt: time.Unix(1, 0)},
		{ID: "newer-auto", Name: "Newer", Prompt: "no", AutoGenerate: true, CreatedAt: time.Unix(3, 0)},
		{ID: "auto", Name: "Resumen", Model: "template-model", Prompt: "Resume en español.", IncludeSpeakerInfo: true, AutoGenerate: true, CreatedAt: time.Unix(2, 0)},
	}}
	jobs := &fakeJobs{job: models.TranscriptionJob{ID: "job-1", Status: models.StatusCompleted, Transcript: transcriptJSON()}}
	service := &fakeLLM{}

	require.NoError(t, newSummarizer(templates, jobs, service).Summarize(context.Background(), "job-1"))

	require.Len(t, service.messages, 1)
	require.Equal(t, "Transcript (with speaker labels - each line is prefixed with [SPEAKER_NAME]):\n[Ana] Hola.\n[SPEAKER_01] Adiós.\n\nInstructions:\nResume en español.", service.messages[0].Content)
	require.Len(t, templates.saved, 1)
	require.Equal(t, "job-1", templates.saved[0].TranscriptionID)
	require.Equal(t, "auto", *templates.saved[0].TemplateID)
	require.Equal(t, "served-model", templates.saved[0].Model)
	require.Equal(t, "## Resumen\nTodo bien.", templates.saved[0].Content)
	require.Equal(t, "## Resumen\nTodo bien.", jobs.cachedSumary)
}

func TestTemplateWithoutSpeakerInfoGetsPlainText(t *testing.T) {
	templates := &fakeTemplates{templates: []models.SummaryTemplate{{ID: "auto", Prompt: "Resume.", AutoGenerate: true}}}
	jobs := &fakeJobs{job: models.TranscriptionJob{ID: "job-1", Status: models.StatusCompleted, Transcript: transcriptJSON()}}
	service := &fakeLLM{}

	require.NoError(t, newSummarizer(templates, jobs, service).Summarize(context.Background(), "job-1"))

	require.Equal(t, "Transcript:\nHola. Adiós.\n\nInstructions:\nResume.", service.messages[0].Content)
}

func TestNoFlaggedTemplateMeansNoSummary(t *testing.T) {
	templates := &fakeTemplates{templates: []models.SummaryTemplate{{ID: "manual", Prompt: "no"}}}
	jobs := &fakeJobs{job: models.TranscriptionJob{ID: "job-1", Status: models.StatusCompleted, Transcript: transcriptJSON()}}
	service := &fakeLLM{}

	require.NoError(t, newSummarizer(templates, jobs, service).Summarize(context.Background(), "job-1"))

	require.Nil(t, service.messages)
	require.Empty(t, templates.saved)
}

func TestUnreachableLLMSavesNothingAndReportsWhy(t *testing.T) {
	templates := &fakeTemplates{templates: []models.SummaryTemplate{{ID: "auto", Prompt: "Resume.", AutoGenerate: true}}}
	jobs := &fakeJobs{job: models.TranscriptionJob{ID: "job-1", Status: models.StatusCompleted, Transcript: transcriptJSON()}}
	service := &fakeLLM{err: errors.New("no Ollama endpoint is reachable")}

	err := newSummarizer(templates, jobs, service).Summarize(context.Background(), "job-1")

	require.ErrorContains(t, err, "no Ollama endpoint is reachable")
	require.Empty(t, templates.saved)
	require.Empty(t, jobs.cachedSumary)
}
