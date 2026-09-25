package adapter

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"
)

type inertWriteCloser struct{}

func (inertWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (inertWriteCloser) Close() error                { return nil }

type inertReadCloser struct{ io.Reader }

func (inertReadCloser) Close() error { return nil }

func TestCloseErrorIsStableWhenReapCannotBeProven(t *testing.T) {
	want := errors.New("tree termination failed")
	original := terminateProcessTreeFn
	terminateProcessTreeFn = func(context.Context, *exec.Cmd) error { return want }
	t.Cleanup(func() { terminateProcessTreeFn = original })

	client := &Client{
		cmd:        &exec.Cmd{},
		stdin:      inertWriteCloser{},
		stdoutPipe: inertReadCloser{Reader: io.LimitReader(nil, 0)},
		closeWait:  time.Millisecond,
		waitDone:   make(chan struct{}),
	}
	first := client.Close()
	second := client.Close()
	if !errors.Is(first, want) || !errors.Is(first, ErrCloseTimeout) {
		t.Fatalf("first Close error = %v", first)
	}
	if first != second {
		t.Fatalf("Close error was not stable: first=%v second=%v", first, second)
	}
}
