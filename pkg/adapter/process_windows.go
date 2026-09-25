package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	processTerminateAccess = 0x0001
	processQueryAccess     = 0x0400
	waitTimeoutMs          = 10000
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// The Go standard library does not expose Windows Job Objects. Use taskkill
// without a shell to request tree termination, then terminate the direct child
// if needed. After sending the close signal, wait up to 10s for clean exit
// before forcefully terminating with TerminateProcess.
func terminateProcessTree(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot != "" {
		taskkillPath := filepath.Join(systemRoot, "System32", "taskkill.exe")
		kill := exec.CommandContext(ctx, taskkillPath, "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		kill.Stdout = io.Discard
		kill.Stderr = io.Discard
		_ = kill.Run()
	}

	if err := waitForProcessExit(cmd.Process); err != nil {
		if terminateErr := terminateProcessDirect(cmd); terminateErr != nil && !errors.Is(terminateErr, os.ErrProcessDone) {
			return fmt.Errorf("terminate adapter process: %w", terminateErr)
		}
	}
	return nil
}

func waitForProcessExit(process *os.Process) error {
	handle, err := windows.OpenProcess(processTerminateAccess|processQueryAccess, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	ret, err := windows.WaitForSingleObject(handle, waitTimeoutMs)
	if err != nil {
		return err
	}
	if ret == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("process did not exit within timeout")
	}
	return nil
}

func terminateProcessDirect(cmd *exec.Cmd) error {
	handle, err := windows.OpenProcess(processTerminateAccess, false, uint32(cmd.Process.Pid))
	if err != nil {
		if isSharingViolation(err) {
			return cmd.Process.Kill()
		}
		return err
	}
	defer windows.CloseHandle(handle)

	ret, _, err := procTerminateProcess.Call(uintptr(handle), uintptr(1))
	if ret == 0 {
		if e, ok := err.(windows.Errno); ok && e == windows.ERROR_SHARING_VIOLATION {
			return cmd.Process.Kill()
		}
		return fmt.Errorf("terminate process: %w", err)
	}
	return nil
}

func isSharingViolation(err error) bool {
	if e, ok := err.(windows.Errno); ok {
		return e == windows.ERROR_SHARING_VIOLATION
	}
	return false
}

var (
	modkernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procTerminateProcess = modkernel32.NewProc("TerminateProcess")
)
