//go:build windows

// The lab launcher contains Ether and every descendant in a kill-on-close job.
// Ether starts suspended and is assigned before any user code can spawn children.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")
var terminateJob = kernel.NewProc("TerminateJobObject")
var resumeThread = kernel.NewProc("ResumeThread")

type basicLimits struct {
	UserTime, JobTime      int64
	Flags                  uint32
	MinWorking, MaxWorking uintptr
	ActiveProcesses        uint32
	Affinity               uintptr
	Priority, Scheduling   uint32
}
type ioCounters struct{ ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes uint64 }
type extendedLimits struct {
	Basic                                                      basicLimits
	IO                                                         ioCounters
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func run() error {
	mode := ""
	if len(os.Args) > 1 {
		if len(os.Args) != 2 {
			return fmt.Errorf("expected no arguments or one reviewed launcher mode")
		}
		mode = os.Args[1]
		switch mode {
		case "--self-test", "--self-test-ffmpeg", "--version-probe":
		default:
			return fmt.Errorf("unsupported launcher mode %q", mode)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(executable)
	if root != `C:\ether-gpu-lab` && root != `D:\ether-gpu-lab` {
		return fmt.Errorf("launcher must reside in owned C:\\ether-gpu-lab or D:\\ether-gpu-lab")
	}
	ether := filepath.Join(root, "ether.exe")
	childCommand := `"` + ether + `"`
	if mode == "--self-test" {
		ether = executable
		childCommand = `"` + ether + `" --probe-child`
	} else if mode == "--self-test-ffmpeg" {
		if _, err := os.Stat(filepath.Join(root, "ffmpeg-8.1.3-1", "ffmpeg.exe")); err != nil {
			return fmt.Errorf("stage pinned lab FFmpeg 8.1.3-1 first")
		}
		ether = executable
		childCommand = `"` + ether + `" --probe-ffmpeg`
	} else if _, err := os.Stat(ether); err != nil {
		return fmt.Errorf("stage reviewed ether.exe first")
	}
	if mode == "--version-probe" {
		childCommand += " version"
	}
	j, _, err := createJob.Call(0, 0)
	if j == 0 {
		return fmt.Errorf("create owned job: %w", err)
	}
	job := syscall.Handle(j)
	defer syscall.CloseHandle(job) // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE covers crash/kill too.
	if err := syscall.SetHandleInformation(job, 1, 0); err != nil {
		return fmt.Errorf("make owned job noninheritable: %w", err)
	}
	limits := extendedLimits{Basic: basicLimits{Flags: 0x2000}}
	ok, _, err := setJob.Call(j, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return fmt.Errorf("set kill-on-close job: %w", err)
	}
	// libc initializes its standard files before main. Supply a valid EOF-only
	// stdin even without a console; INVALID_HANDLE_VALUE makes that init panic.
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open owned null stdin: %w", err)
	}
	defer stdin.Close()
	// Only the null stdin and redirected logs are made inheritable. The job
	// handle remains noninheritable so no child can keep the job alive.
	for _, h := range []syscall.Handle{syscall.Handle(stdin.Fd()), syscall.Handle(os.Stdout.Fd()), syscall.Handle(os.Stderr.Fd())} {
		if err := syscall.SetHandleInformation(h, 1, 1); err != nil {
			return fmt.Errorf("prepare owned standard handle: %w", err)
		}
	}
	startup := syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: syscall.STARTF_USESTDHANDLES,
		StdInput: syscall.Handle(stdin.Fd()), StdOutput: syscall.Handle(os.Stdout.Fd()), StdErr: syscall.Handle(os.Stderr.Fd())}
	var process syscall.ProcessInformation
	application, _ := syscall.UTF16PtrFromString(ether)
	command, _ := syscall.UTF16PtrFromString(childCommand)
	directory, _ := syscall.UTF16PtrFromString(root)
	// CREATE_SUSPENDED | CREATE_NO_WINDOW | BELOW_NORMAL_PRIORITY_CLASS.
	if err := syscall.CreateProcess(application, command, nil, nil, true, 0x4|0x08000000|0x4000, nil, directory, &startup, &process); err != nil {
		return fmt.Errorf("create suspended lab child: %w", err)
	}
	defer syscall.CloseHandle(process.Process)
	defer syscall.CloseHandle(process.Thread)
	assigned := false
	defer func() {
		if assigned {
			terminateJob.Call(j, 1)
		} else {
			syscall.TerminateProcess(process.Process, 1)
		}
		syscall.WaitForSingleObject(process.Process, 10000)
	}()
	ok, _, err = assignJob.Call(j, uintptr(process.Process))
	if ok == 0 {
		return fmt.Errorf("assign suspended child to owned job: %w", err)
	}
	assigned = true
	// Record child identity before allowing it to execute. A file failure causes
	// the exact suspended process/job to terminate; no unknown child can escape.
	if err := os.WriteFile(filepath.Join(root, "specialist.child.pid"), []byte(fmt.Sprint(process.ProcessId)), 0600); err != nil {
		return err
	}
	result, _, err := resumeThread.Call(uintptr(process.Thread))
	if uint32(result) == 0xffffffff {
		return fmt.Errorf("resume owned child: %w", err)
	}
	fmt.Printf("owned native specialist child PID=%d; launcher PID=%d\n", process.ProcessId, os.Getpid())
	result32, err := syscall.WaitForSingleObject(process.Process, syscall.INFINITE)
	if err != nil || result32 != syscall.WAIT_OBJECT_0 {
		return fmt.Errorf("wait for owned specialist: result=%d error=%v", result32, err)
	}
	var status uint32
	if err := syscall.GetExitCodeProcess(process.Process, &status); err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("owned specialist exited with status %d", status)
	}
	return nil
}

// Test modes start only copies of this binary or the fixed pinned lab FFmpeg.
// They allow native job-tree verification before Ether or credentials are staged.
func probe() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(executable)
	if root != `C:\ether-gpu-lab` && root != `D:\ether-gpu-lab` {
		return fmt.Errorf("probe must reside in owned lab")
	}
	if os.Args[1] == "--probe-leaf" {
		if err := os.WriteFile(filepath.Join(root, "probe.leaf.pid"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
			return err
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if os.Args[1] == "--probe-ffmpeg" {
		// A real NVENC child with a fixed synthetic source and null sink. The
		// realtime input gives the owner time to inspect/kill the exact tree;
		// both media length and an independent wall deadline bound this test.
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, filepath.Join(root, "ffmpeg-8.1.3-1", "ffmpeg.exe"),
			"-hide_banner", "-nostdin", "-v", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
			"-t", "30", "-frames:v", "720", "-c:v", "h264_nvenc", "-progress", "pipe:1", "-stats_period", "1", "-f", "null", "-")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.WaitDelay = 3 * time.Second
		if err := child.Start(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "probe.ffmpeg.pid"), []byte(fmt.Sprint(child.Process.Pid)), 0600); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			return err
		}
		fmt.Printf("owned pinned NVENC descendant PID=%d; branch PID=%d\n", child.Process.Pid, os.Getpid())
		return child.Wait()
	}
	child := exec.Command(executable, "--probe-leaf")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		return err
	}
	return child.Wait()
}

func main() {
	var err error
	if len(os.Args) == 2 && (os.Args[1] == "--probe-child" || os.Args[1] == "--probe-leaf" || os.Args[1] == "--probe-ffmpeg") {
		err = probe()
	} else {
		err = run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
