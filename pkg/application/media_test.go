package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xibodev/gflow/pkg/application"
)

type blockingBackend struct {
	started chan struct{}
	release chan struct{}
}

type immediateBackend struct{}

type closeErrorBackend struct {
	immediateBackend
	err   error
	calls int
}

func (b *closeErrorBackend) Close() error {
	b.calls++
	return b.err
}

func (immediateBackend) GenerateImage(context.Context, application.ImageRequest) (application.ImageOutput, error) {
	return application.ImageOutput{}, nil
}

func (immediateBackend) GenerateVideo(_ context.Context, request application.VideoRequest) (application.VideoOutput, error) {
	return application.VideoOutput{FilePath: request.Prompt + ".mp4"}, nil
}

func (immediateBackend) UpsampleVideo(context.Context, application.UpsampleRequest) (application.UpsampleOutput, error) {
	return application.UpsampleOutput{}, nil
}

func (b *blockingBackend) GenerateImage(context.Context, application.ImageRequest) (application.ImageOutput, error) {
	return application.ImageOutput{}, nil
}

func (b *blockingBackend) GenerateVideo(ctx context.Context, _ application.VideoRequest) (application.VideoOutput, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
		return application.VideoOutput{FilePath: "video.mp4", Resolution: "720p", HasAudio: true}, nil
	case <-ctx.Done():
		return application.VideoOutput{}, ctx.Err()
	}
}

func (b *blockingBackend) UpsampleVideo(context.Context, application.UpsampleRequest) (application.UpsampleOutput, error) {
	return application.UpsampleOutput{}, nil
}

func TestMediaServiceEnqueueReturnsCapacityErrorWithoutBlocking(t *testing.T) {
	backend := &blockingBackend{started: make(chan struct{}, 1), release: make(chan struct{})}
	service := application.NewMediaService(backend, 1)
	t.Cleanup(func() { _ = service.Close() })

	if _, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: "active"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if _, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: "queued"}); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: "rejected"})
	if !errors.Is(err, application.ErrQueueFull) {
		t.Fatalf("error = %v, want ErrQueueFull", err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("full queue enqueue blocked")
	}
}

func TestMediaServiceCloseCancelsWorkerAndRejectsWork(t *testing.T) {
	backend := &blockingBackend{started: make(chan struct{}, 1), release: make(chan struct{})}
	service := application.NewMediaService(backend, 1)
	job, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: "active"})
	if err != nil {
		t.Fatal(err)
	}
	<-backend.started

	done := make(chan struct{})
	go func() {
		_ = service.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel active generation")
	}
	if _, err := service.EnqueueVideo(context.Background(), application.VideoRequest{}); !errors.Is(err, application.ErrServiceClosed) {
		t.Fatalf("enqueue after Close error = %v, want ErrServiceClosed", err)
	}
	got, ok := service.VideoJob(job.ID)
	if !ok || got.Status != "failed" {
		t.Fatalf("cancelled job = %+v, found=%v", got, ok)
	}
}

func TestMediaServiceCloseErrorIsDurable(t *testing.T) {
	want := errors.New("backend close failed")
	backend := &closeErrorBackend{err: want}
	service := application.NewMediaService(backend, 1)
	first := service.Close()
	second := service.Close()
	if !errors.Is(first, want) || first != second {
		t.Fatalf("Close errors = %v, %v; want stable %v", first, second, want)
	}
	if backend.calls != 1 {
		t.Fatalf("backend Close calls = %d, want 1", backend.calls)
	}
}

func TestMediaServiceJobIDsAreUniqueConcurrently(t *testing.T) {
	service := application.NewMediaService(immediateBackend{}, 256)
	t.Cleanup(func() { _ = service.Close() })
	const count = 200
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			job, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: fmt.Sprintf("video-%d", i)})
			if err != nil {
				errs <- err
				return
			}
			ids <- job.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[string]bool, count)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate job ID %q", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("unique IDs = %d, want %d", len(seen), count)
	}
}

func TestMediaServiceBoundsTerminalJobsWithoutEvictingActive(t *testing.T) {
	backend := &blockingBackend{started: make(chan struct{}, 1), release: make(chan struct{})}
	service := application.NewMediaServiceWithTerminalRetention(backend, 4, 2)
	t.Cleanup(func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
		_ = service.Close()
	})
	active, err := service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: "active"})
	if err != nil {
		t.Fatal(err)
	}
	<-backend.started
	queued := make([]application.VideoJob, 3)
	for i := range queued {
		queued[i], err = service.EnqueueVideo(context.Background(), application.VideoRequest{Prompt: fmt.Sprintf("queued-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	close(backend.release)
	deadline := time.Now().Add(time.Second)
	for {
		last, ok := service.VideoJob(queued[2].ID)
		if ok && last.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("jobs did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	if _, ok := service.VideoJob(active.ID); ok {
		t.Fatal("oldest terminal job was retained past bound")
	}
	if _, ok := service.VideoJob(queued[0].ID); ok {
		t.Fatal("second-oldest terminal job was retained past bound")
	}
	for _, job := range queued[1:] {
		if _, ok := service.VideoJob(job.ID); !ok {
			t.Fatalf("recent terminal job %q was evicted", job.ID)
		}
	}
}
