package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/queue"
	"scriberr/internal/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type QueueCancelTestSuite struct {
	suite.Suite
	helper  *TestHelper
	jobRepo repository.JobRepository
}

func (suite *QueueCancelTestSuite) SetupSuite() {
	suite.helper = NewTestHelper(suite.T(), "queue_cancel_test.db")
	suite.jobRepo = repository.NewJobRepository(suite.helper.DB)
}

func (suite *QueueCancelTestSuite) TearDownSuite() {
	suite.helper.Cleanup()
}

func (suite *QueueCancelTestSuite) SetupTest() {
	suite.helper.ResetDB(suite.T())
}

// A pending job waiting in the queue behind a busy worker can be cancelled:
// KillJob succeeds, the job is marked failed with a queued-cancellation
// message, and the worker never processes it when it later surfaces from
// the channel.
func (suite *QueueCancelTestSuite) TestKillQueuedJobCancelsWithoutProcessing() {
	runningJob := suite.helper.CreateTestTranscriptionJob(suite.T(), "Occupies the single worker")
	queuedJob := suite.helper.CreateTestTranscriptionJob(suite.T(), "Waits in queue, gets cancelled")

	mockProcessor := &MockJobProcessor{}
	mockProcessor.processDelay = 400 * time.Millisecond
	mockProcessor.On("ProcessJobWithProcess", mock.Anything, runningJob.ID).Return(nil)
	mockProcessor.On("ProcessJobWithProcess", mock.Anything, queuedJob.ID).Return(nil).Maybe()

	tq := queue.NewTaskQueue(1, mockProcessor, suite.jobRepo)
	tq.Start()
	defer tq.Stop()

	assert.NoError(suite.T(), tq.EnqueueJob(runningJob.ID))
	assert.Eventually(suite.T(), func() bool {
		return tq.IsJobRunning(runningJob.ID)
	}, 2*time.Second, 20*time.Millisecond, "first job should occupy the worker")

	assert.NoError(suite.T(), tq.EnqueueJob(queuedJob.ID))

	// The queued job is not running: cancelling it must succeed anyway.
	err := tq.KillJob(queuedJob.ID)
	assert.NoError(suite.T(), err, "cancelling a queued (pending) job should succeed")

	// The cancelled job is immediately failed with a queued-cancellation message.
	assert.Eventually(suite.T(), func() bool {
		job, ferr := suite.jobRepo.FindByID(context.Background(), queuedJob.ID)
		return ferr == nil && job.Status == models.StatusFailed &&
			job.ErrorMessage != nil && strings.Contains(strings.ToLower(*job.ErrorMessage), "queued")
	}, 2*time.Second, 50*time.Millisecond, "cancelled queued job should be failed with a queued-cancel message")

	// The running job is unaffected and completes normally.
	assert.Eventually(suite.T(), func() bool {
		job, ferr := suite.jobRepo.FindByID(context.Background(), runningJob.ID)
		return ferr == nil && job.Status == models.StatusCompleted
	}, 3*time.Second, 50*time.Millisecond, "running job should complete normally")

	// Give the worker time to drain the channel entry for the cancelled job.
	time.Sleep(300 * time.Millisecond)

	mockProcessor.AssertNotCalled(suite.T(), "ProcessJobWithProcess", mock.Anything, queuedJob.ID)

	// The cancelled job was not resurrected by the worker.
	job, ferr := suite.jobRepo.FindByID(context.Background(), queuedJob.ID)
	assert.NoError(suite.T(), ferr)
	assert.Equal(suite.T(), models.StatusFailed, job.Status)
}

// A pending job deleted while still waiting in the queue channel is skipped
// by the worker when its ID surfaces: it is never processed and never
// resurrected into processing state.
func (suite *QueueCancelTestSuite) TestWorkerSkipsQueuedJobDeletedBeforeDequeue() {
	runningJob := suite.helper.CreateTestTranscriptionJob(suite.T(), "Occupies the single worker")
	deletedJob := suite.helper.CreateTestTranscriptionJob(suite.T(), "Deleted while queued")

	mockProcessor := &MockJobProcessor{}
	mockProcessor.processDelay = 400 * time.Millisecond
	mockProcessor.On("ProcessJobWithProcess", mock.Anything, runningJob.ID).Return(nil)
	mockProcessor.On("ProcessJobWithProcess", mock.Anything, deletedJob.ID).Return(nil).Maybe()

	tq := queue.NewTaskQueue(1, mockProcessor, suite.jobRepo)
	tq.Start()
	defer tq.Stop()

	assert.NoError(suite.T(), tq.EnqueueJob(runningJob.ID))
	assert.Eventually(suite.T(), func() bool {
		return tq.IsJobRunning(runningJob.ID)
	}, 2*time.Second, 20*time.Millisecond, "first job should occupy the worker")

	assert.NoError(suite.T(), tq.EnqueueJob(deletedJob.ID))

	// Delete the queued job while its ID is still in the channel.
	assert.NoError(suite.T(), suite.jobRepo.Delete(context.Background(), deletedJob.ID))

	assert.Eventually(suite.T(), func() bool {
		job, ferr := suite.jobRepo.FindByID(context.Background(), runningJob.ID)
		return ferr == nil && job.Status == models.StatusCompleted
	}, 3*time.Second, 50*time.Millisecond, "running job should complete normally")

	time.Sleep(300 * time.Millisecond)

	mockProcessor.AssertNotCalled(suite.T(), "ProcessJobWithProcess", mock.Anything, deletedJob.ID)

	// The deleted job stays deleted; the worker did not write it back.
	_, ferr := suite.jobRepo.FindByID(context.Background(), deletedJob.ID)
	assert.Error(suite.T(), ferr, "deleted job should remain deleted after its channel entry drains")
}

func TestQueueCancelTestSuite(t *testing.T) {
	suite.Run(t, new(QueueCancelTestSuite))
}
