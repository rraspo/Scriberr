// Package summary writes a transcription's summary on its own, right after the
// transcription completes, with the template flagged auto_generate.
package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"scriberr/internal/llm"
	"scriberr/internal/models"
)

type templateStore interface {
	List(ctx context.Context, offset, limit int) ([]models.SummaryTemplate, int64, error)
	SaveSummary(ctx context.Context, summary *models.Summary) error
}

type jobStore interface {
	FindByID(ctx context.Context, id interface{}) (*models.TranscriptionJob, error)
	UpdateSummary(ctx context.Context, jobID string, summary string) error
}

type speakerStore interface {
	ListByJob(ctx context.Context, jobID string) ([]models.SpeakerMapping, error)
}

type AutoSummarizer struct {
	templates templateStore
	jobs      jobStore
	speakers  speakerStore
	service   func(context.Context) (llm.Service, error)
}

func NewAutoSummarizer(templates templateStore, jobs jobStore, speakers speakerStore, service func(context.Context) (llm.Service, error)) *AutoSummarizer {
	return &AutoSummarizer{templates: templates, jobs: jobs, speakers: speakers, service: service}
}

// Summarize writes the job's summary with the auto_generate template. It does
// nothing when no template is flagged or the job has no transcript.
func (a *AutoSummarizer) Summarize(ctx context.Context, jobID string) error {
	template, err := a.autoTemplate(ctx)
	if err != nil || template == nil {
		return err
	}
	job, err := a.jobs.FindByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}
	if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
		return nil
	}
	content, err := a.promptContent(ctx, job, template)
	if err != nil {
		return err
	}
	service, err := a.service(ctx)
	if err != nil {
		return err
	}
	response, err := service.ChatCompletion(ctx, template.Model, []llm.ChatMessage{{Role: "user", Content: content}}, 0)
	if err != nil {
		return err
	}
	if len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return fmt.Errorf("the model returned an empty summary")
	}
	text := response.Choices[0].Message.Content
	model := response.Model
	if model == "" {
		model = template.Model
	}
	if err := a.templates.SaveSummary(ctx, &models.Summary{TranscriptionID: jobID, TemplateID: &template.ID, Model: model, Content: text}); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}
	return a.jobs.UpdateSummary(ctx, jobID, text)
}

func (a *AutoSummarizer) autoTemplate(ctx context.Context) (*models.SummaryTemplate, error) {
	templates, _, err := a.templates.List(ctx, 0, 1000)
	if err != nil {
		return nil, fmt.Errorf("list summary templates: %w", err)
	}
	var flagged []models.SummaryTemplate
	for _, template := range templates {
		if template.AutoGenerate {
			flagged = append(flagged, template)
		}
	}
	if len(flagged) == 0 {
		return nil, nil
	}
	sort.SliceStable(flagged, func(left, right int) bool { return flagged[left].CreatedAt.Before(flagged[right].CreatedAt) })
	return &flagged[0], nil
}

// promptContent builds the same text the summary dialog sends, so a summary
// written automatically reads like one asked for by hand.
func (a *AutoSummarizer) promptContent(ctx context.Context, job *models.TranscriptionJob, template *models.SummaryTemplate) (string, error) {
	var transcript struct {
		Text     string `json:"text"`
		Segments []struct {
			Text    string  `json:"text"`
			Speaker *string `json:"speaker"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(*job.Transcript), &transcript); err != nil {
		return "", fmt.Errorf("read transcript: %w", err)
	}
	if !template.IncludeSpeakerInfo || len(transcript.Segments) == 0 {
		return "Transcript:\n" + transcript.Text + "\n\nInstructions:\n" + template.Prompt, nil
	}
	mappings, err := a.speakers.ListByJob(ctx, job.ID)
	if err != nil {
		return "", fmt.Errorf("load speaker names: %w", err)
	}
	names := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		names[mapping.OriginalSpeaker] = mapping.CustomName
	}
	lines := make([]string, len(transcript.Segments))
	for index, segment := range transcript.Segments {
		speaker := "UNKNOWN"
		if segment.Speaker != nil && *segment.Speaker != "" {
			speaker = *segment.Speaker
		}
		if name := names[speaker]; name != "" {
			speaker = name
		}
		lines[index] = "[" + speaker + "] " + strings.TrimSpace(segment.Text)
	}
	return "Transcript (with speaker labels - each line is prefixed with [SPEAKER_NAME]):\n" + strings.Join(lines, "\n") +
		"\n\nInstructions:\n" + template.Prompt, nil
}
