package queue

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"scriberr/internal/models"
	"scriberr/internal/repository"
)

type instantProcessor struct{}

func (instantProcessor) ProcessJob(ctx context.Context, jobID string) error { return nil }
func (instantProcessor) ProcessJobWithProcess(ctx context.Context, jobID string, registerProcess func(*exec.Cmd)) error {
	return nil
}

func newQueueWithJobs(t *testing.T, jobIDs ...string) (*TaskQueue, repository.JobRepository) {
	t.Helper()
	t.Setenv("QUEUE_WORKERS", "1")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}))
	jobs := repository.NewJobRepository(db)
	for _, id := range jobIDs {
		require.NoError(t, jobs.Create(context.Background(), &models.TranscriptionJob{ID: id, AudioPath: "/dev/null", Status: models.StatusUploaded}))
	}
	return NewTaskQueue(1, instantProcessor{}, jobs), jobs
}

func waitForStatus(t *testing.T, jobs repository.JobRepository, jobID string, want models.JobStatus) {
	t.Helper()
	require.Eventually(t, func() bool {
		job, err := jobs.FindByID(context.Background(), jobID)
		return err == nil && job.Status == want
	}, 3*time.Second, 20*time.Millisecond, "job %s never reached %s", jobID, want)
}

// A slow after-completion step (the automatic summary on a slow LLM host)
// must not hold the only worker: the next queued job still gets transcribed.
func TestSlowAfterCompletionDoesNotBlockTheNextJob(t *testing.T) {
	queue, jobs := newQueueWithJobs(t, "first", "second")
	// Blocks until the queue stops, as a real LLM call does until Stop
	// cancels its context.
	queue.SetAfterCompletion(func(ctx context.Context, jobID string) {
		if jobID == "first" {
			<-ctx.Done()
		}
	})
	queue.Start()
	defer queue.Stop()

	require.NoError(t, jobs.UpdateStatus(context.Background(), "first", models.StatusPending))
	require.NoError(t, queue.EnqueueJob("first"))
	waitForStatus(t, jobs, "first", models.StatusCompleted)

	require.NoError(t, jobs.UpdateStatus(context.Background(), "second", models.StatusPending))
	require.NoError(t, queue.EnqueueJob("second"))
	waitForStatus(t, jobs, "second", models.StatusCompleted)
}

// After-completion steps still run one at a time, in completion order, so
// summaries never pile onto the shared LLM host.
func TestAfterCompletionStepsRunOneAtATime(t *testing.T) {
	queue, jobs := newQueueWithJobs(t, "first", "second")
	ran := make(chan string, 2)
	active := make(chan struct{}, 1)
	queue.SetAfterCompletion(func(ctx context.Context, jobID string) {
		select {
		case active <- struct{}{}:
		default:
			t.Errorf("after-completion for %s overlapped another", jobID)
		}
		time.Sleep(50 * time.Millisecond)
		<-active
		ran <- jobID
	})
	queue.Start()
	defer queue.Stop()

	for _, id := range []string{"first", "second"} {
		require.NoError(t, jobs.UpdateStatus(context.Background(), id, models.StatusPending))
		require.NoError(t, queue.EnqueueJob(id))
	}
	require.Equal(t, "first", <-ran)
	require.Equal(t, "second", <-ran)
}
