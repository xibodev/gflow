package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQueueFull     = errors.New("video queue is at capacity")
	ErrServiceClosed = errors.New("media service is closed")
	nextVideoJobID   atomic.Uint64
)

const (
	DefaultVideoQueueCapacity   = 100
	DefaultTerminalJobRetention = 1000
)

// MediaBackend performs provider-specific media operations. Implementations may
// use remote services, browser automation, or direct provider clients.
type MediaBackend interface {
	GenerateImage(context.Context, ImageRequest) (ImageOutput, error)
	GenerateVideo(context.Context, VideoRequest) (VideoOutput, error)
	UpsampleVideo(context.Context, UpsampleRequest) (UpsampleOutput, error)
}

// ReferenceResolver optionally resolves local paths or provider media IDs.
type ReferenceResolver interface {
	ResolveReference(context.Context, string) (string, error)
}

// StatusProvider optionally supplies provider readiness and quota status.
type StatusProvider interface {
	ProviderStatus(context.Context, int) (StatusOutput, error)
}

// BackendCloser lets a MediaService own an out-of-process backend lifecycle.
type BackendCloser interface {
	Close() error
}

type ImageOutput struct {
	FilePath string
	Files    []string
	Warnings []string
}

type VideoOutput struct {
	FilePath   string
	Resolution string
	HasAudio   bool
}

type UpsampleRequest struct {
	MediaID    string
	Aspect     string
	Resolution string
	Seed       *int64
}

type UpsampleOutput struct {
	Files []string
}

// StatusOutput supports structured provider status and legacy text status.
type StatusOutput struct {
	Data any
	Text string
}

// VideoJob is an immutable snapshot returned to callers.
type VideoJob struct {
	ID          string
	Status      string
	Prompt      string
	Aspect      string
	Duration    int
	Resolution  string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	FilePath    string
	HasAudio    bool
	Error       string
	QueueDepth  int

	request VideoRequest
}

// MediaCapabilities is the application-facing contract used by transport wrappers.
type MediaCapabilities interface {
	GenerateImage(context.Context, ImageRequest) (ImageOutput, error)
	EnqueueVideo(context.Context, VideoRequest) (VideoJob, error)
	VideoJob(string) (VideoJob, bool)
	UpsampleVideo(context.Context, UpsampleRequest) (UpsampleOutput, error)
	Status(context.Context) (StatusOutput, error)
	Close() error
}

type MediaService struct {
	backend MediaBackend
	queue   chan string
	slots   chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc

	mu     sync.RWMutex
	jobs   map[string]*VideoJob
	closed bool

	terminalRetention int
	terminalJobs      []string

	closeOnce sync.Once
	closeErr  error
	wg        sync.WaitGroup
}

func NewMediaService(backend MediaBackend, queueCapacity int) *MediaService {
	return NewMediaServiceWithTerminalRetention(backend, queueCapacity, DefaultTerminalJobRetention)
}

// NewMediaServiceWithTerminalRetention creates a service with an explicit
// terminal-job history bound. Active jobs are never evicted.
func NewMediaServiceWithTerminalRetention(backend MediaBackend, queueCapacity, terminalRetention int) *MediaService {
	if queueCapacity < 0 {
		queueCapacity = 0
	}
	if terminalRetention < 0 {
		terminalRetention = 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &MediaService{
		backend:           backend,
		queue:             make(chan string, queueCapacity),
		slots:             make(chan struct{}, queueCapacity),
		ctx:               ctx,
		cancel:            cancel,
		jobs:              make(map[string]*VideoJob),
		terminalRetention: terminalRetention,
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *MediaService) GenerateImage(ctx context.Context, request ImageRequest) (ImageOutput, error) {
	if err := s.checkOpen(); err != nil {
		return ImageOutput{}, err
	}
	request, err := NormalizeImageRequest(request)
	if err != nil {
		return ImageOutput{}, err
	}
	if resolver, ok := s.backend.(ReferenceResolver); ok {
		request.Reference, err = resolver.ResolveReference(ctx, request.Reference)
		if err != nil {
			return ImageOutput{}, err
		}
		if request.Reference != "" {
			request.ReferenceMediaIDs = append(request.ReferenceMediaIDs, request.Reference)
		}
	}
	return s.backend.GenerateImage(ctx, request)
}

// EnqueueVideo accepts work without waiting for queue capacity.
func (s *MediaService) EnqueueVideo(ctx context.Context, request VideoRequest) (VideoJob, error) {
	if err := s.checkOpen(); err != nil {
		return VideoJob{}, err
	}
	request, err := NormalizeVideoRequest(request)
	if err != nil {
		return VideoJob{}, err
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return VideoJob{}, ErrQueueFull
	}
	reserved := true
	defer func() {
		if reserved {
			<-s.slots
		}
	}()

	if resolver, ok := s.backend.(ReferenceResolver); ok {
		request.Start, err = resolver.ResolveReference(ctx, request.Start)
		if err != nil {
			return VideoJob{}, err
		}
		request.End, err = resolver.ResolveReference(ctx, request.End)
		if err != nil {
			return VideoJob{}, err
		}
	}

	now := time.Now()
	job := &VideoJob{
		ID:         fmt.Sprintf("job_vid_%d", nextVideoJobID.Add(1)),
		Status:     "queued",
		Prompt:     request.Prompt,
		Aspect:     request.Aspect,
		Duration:   request.Duration,
		Resolution: request.Resolution,
		CreatedAt:  now,
		request:    request,
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return VideoJob{}, ErrServiceClosed
	}
	job.QueueDepth = len(s.slots)
	s.jobs[job.ID] = job
	s.queue <- job.ID
	reserved = false
	result := *job
	s.mu.Unlock()
	return result, nil
}

func (s *MediaService) VideoJob(id string) (VideoJob, bool) {
	s.mu.RLock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.RUnlock()
		return VideoJob{}, false
	}
	snapshot := *job
	s.mu.RUnlock()
	return snapshot, true
}

func (s *MediaService) UpsampleVideo(ctx context.Context, request UpsampleRequest) (UpsampleOutput, error) {
	if err := s.checkOpen(); err != nil {
		return UpsampleOutput{}, err
	}
	return s.backend.UpsampleVideo(ctx, request)
}

func (s *MediaService) Status(ctx context.Context) (StatusOutput, error) {
	provider, ok := s.backend.(StatusProvider)
	if !ok {
		return StatusOutput{}, nil
	}
	s.mu.RLock()
	active := 0
	for _, job := range s.jobs {
		if job.Status == "queued" || job.Status == "processing" {
			active++
		}
	}
	s.mu.RUnlock()
	return provider.ProviderStatus(ctx, active)
}

func (s *MediaService) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		s.wg.Wait()
		if closer, ok := s.backend.(BackendCloser); ok {
			err := closer.Close()
			s.mu.Lock()
			s.closeErr = err
			s.mu.Unlock()
		}
	})
	s.mu.RLock()
	err := s.closeErr
	s.mu.RUnlock()
	return err
}

func (s *MediaService) checkOpen() error {
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return ErrServiceClosed
	}
	return nil
}

func (s *MediaService) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			s.failQueued(ErrServiceClosed)
			return
		case id := <-s.queue:
			<-s.slots
			s.runJob(id)
		}
	}
}

func (s *MediaService) runJob(id string) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	job.StartedAt = &now
	job.Status = "processing"
	request := job.request
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
	result, err := func() (result VideoOutput, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("worker error: %v", recovered)
			}
		}()
		return s.backend.GenerateVideo(ctx, request)
	}()
	cancel()

	completedAt := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	job.CompletedAt = &completedAt
	if err != nil {
		job.Error = err.Error()
		job.Status = "failed"
		s.retainTerminalLocked(job.ID)
		return
	}
	if result.FilePath == "" {
		job.Error = "generation produced no downloadable assets"
		job.Status = "failed"
		s.retainTerminalLocked(job.ID)
		return
	}
	job.FilePath = result.FilePath
	job.Resolution = result.Resolution
	job.HasAudio = result.HasAudio
	job.Status = "completed"
	s.retainTerminalLocked(job.ID)
}

func (s *MediaService) failQueued(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, job := range s.jobs {
		if job.Status == "queued" {
			job.Status = "failed"
			job.Error = err.Error()
			job.CompletedAt = &now
			s.retainTerminalLocked(job.ID)
		}
	}
}

func (s *MediaService) retainTerminalLocked(id string) {
	s.terminalJobs = append(s.terminalJobs, id)
	for len(s.terminalJobs) > s.terminalRetention {
		oldest := s.terminalJobs[0]
		s.terminalJobs = s.terminalJobs[1:]
		if job, ok := s.jobs[oldest]; ok && job.Status != "queued" && job.Status != "processing" {
			delete(s.jobs, oldest)
		}
	}
}
