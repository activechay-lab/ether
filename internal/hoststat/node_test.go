package hoststat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type nodeSamplerFunc func(context.Context) (NodeReading, error)

func (f nodeSamplerFunc) Sample(ctx context.Context) (NodeReading, error) { return f(ctx) }

type gpuProviderFunc func(context.Context) ([]GPUReading, error)

func (f gpuProviderFunc) SampleGPU(ctx context.Context) ([]GPUReading, error) { return f(ctx) }

func TestNodeSnapshotOwnershipFreshnessAndBounds(t *testing.T) {
	at := time.Now()
	clock := at
	calls := 0
	reading := NodeReading{HostCPUPercent: observed(0.0, at), ProcessRSSBytes: observed(uint64(42), at)}
	devices := make([]GPUReading, MaxGPUDevices+3)
	for i := range devices {
		devices[i] = GPUReading{Device: strings.Repeat("d", 300), Capabilities: []string{"h264"}, UtilizationPercent: observed(0.0, at)}
	}
	paths := []string{t.TempDir()}
	r, err := NewNodeResources(NodeOptions{Now: func() time.Time { return clock }, ScratchPaths: paths,
		Sampler: nodeSamplerFunc(func(context.Context) (NodeReading, error) { calls++; return reading, nil }),
		GPU:     gpuProviderFunc(func(context.Context) ([]GPUReading, error) { return devices, nil }),
		Filesystem: func(context.Context, string) (FilesystemCapacity, error) {
			calls++
			return FilesystemCapacity{AvailableBytes: observed(uint64(0), at)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := r.Snapshot()
	if before.HostCPUPercent.State != Unavailable || before.Scratch[0].AvailableBytes.Value != nil || calls != 0 {
		t.Fatal("initial snapshot did I/O or invented capacity")
	}
	original := paths[0]
	paths[0] = "mutated"
	r.collect(context.Background())
	first := r.Snapshot()
	if first.HostCPUPercent.State != Available || *first.HostCPUPercent.Value != 0 || first.HostMemoryAvailableBytes.State != Unavailable || first.Scratch[0].Path != original {
		t.Fatalf("scope/zero/unknown: %+v", first)
	}
	if len(first.GPU) != MaxGPUDevices || len(first.GPU[0].Device) != 256 {
		t.Fatal("unbounded GPU output")
	}
	*reading.ProcessRSSBytes.Value = 99
	devices[0].Capabilities[0] = "mutated"
	*first.ProcessRSSBytes.Value = 100
	first.GPU[0].Capabilities[0] = "client mutation"
	*first.Scratch[0].AvailableBytes.Value = 20
	second := r.Snapshot()
	if *second.ProcessRSSBytes.Value != 42 || second.GPU[0].Capabilities[0] != "h264" || *second.Scratch[0].AvailableBytes.Value != 0 {
		t.Fatal("snapshot aliases provider or client memory")
	}
	clock = at.Add(16 * time.Second)
	stale := r.Snapshot()
	if stale.HostCPUPercent.State != Stale || stale.GPUState != Stale || stale.Scratch[0].AvailableBytes.State != Stale || !stale.HostCPUPercent.At.Equal(at) {
		t.Fatal("freshness not derived from observation time")
	}
	if calls != 2 {
		t.Fatalf("reads sampled: %d", calls)
	}
}

func TestNodeErrorsTimeoutAndLifecycle(t *testing.T) {
	at := time.Now()
	r, err := NewNodeResources(NodeOptions{SampleTimeout: time.Millisecond,
		Sampler: nodeSamplerFunc(func(context.Context) (NodeReading, error) {
			return NodeReading{HostCPUPercent: observed(0.0, at)}, errors.New("failed")
		}),
		GPU: gpuProviderFunc(func(ctx context.Context) ([]GPUReading, error) {
			<-ctx.Done()
			return []GPUReading{{Device: "late"}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.collect(context.Background())
	if got := r.Snapshot(); got.HostCPUPercent.State != Unavailable || got.GPUState != Unavailable || len(got.GPU) != 0 {
		t.Fatal("error/expired sample published as fresh")
	}
	entered := make(chan struct{})
	done := make(chan error, 1)
	r, err = NewNodeResources(NodeOptions{Sampler: nodeSamplerFunc(func(ctx context.Context) (NodeReading, error) {
		close(entered)
		<-ctx.Done()
		return NodeReading{}, ctx.Err()
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- r.Run(ctx) }()
	<-entered
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("duplicate sampling loop allowed")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run failed to retire")
	}
	if r.Snapshot().HostCPUPercent.State != Unavailable {
		t.Fatal("cancelled read published")
	}
}

func TestNodeObservationValidation(t *testing.T) {
	at := time.Now()
	cases := []Metric[float64]{{}, {Value: new(float64), State: Available}, {Value: new(float64), At: at.Add(time.Second), State: Available}, {At: at, State: Available}}
	for _, m := range cases {
		if got := cloneMetric(m, at, time.Second); got.State != Unavailable || got.Value != nil {
			t.Fatalf("invented reading %+v", got)
		}
	}
	unlimited := Metric[float64]{At: at, State: Unlimited}
	stale := cloneMetric(unlimited, at.Add(2*time.Second), time.Second)
	if again := cloneMetric(stale, at.Add(3*time.Second), time.Second); again.State != Stale || again.Value != nil {
		t.Fatal("stale unlimited constraint lost observation")
	}
	paths := make([]string, MaxScratchPaths+1)
	if _, err := NewNodeResources(NodeOptions{ScratchPaths: paths}); err == nil {
		t.Fatal("unbounded paths accepted")
	}
	if _, err := NewNodeResources(NodeOptions{ScratchPaths: []string{"relative"}}); err == nil {
		t.Fatal("relative scratch accepted")
	}
}

func TestCgroupV2LocalConstraints(t *testing.T) {
	dir := t.TempDir()
	at := time.Now()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.max", "1048576\n")
	write("memory.current", "0\n")
	write("cpu.max", "150000 100000\n")
	var r NodeReading
	readCgroupV2(dir, at, &r)
	if *r.CgroupMemoryLimitBytes.Value != 1048576 || *r.CgroupMemoryCurrentBytes.Value != 0 || *r.CgroupCPUQuotaCores.Value != 1.5 {
		t.Fatalf("bad limits %+v", r)
	}
	if !reflect.DeepEqual(r.HostMemoryAvailableBytes, Metric[uint64]{}) {
		t.Fatal("cgroup limits confused with host memory")
	}
	write("memory.max", "max")
	write("cpu.max", "max 100000")
	r = NodeReading{}
	readCgroupV2(dir, at, &r)
	if r.CgroupMemoryLimitBytes.State != Unlimited || r.CgroupMemoryLimitBytes.Value != nil || r.CgroupCPUQuotaCores.State != Unlimited {
		t.Fatal("unlimited confused with zero or unknown")
	}
	write("memory.max", strings.Repeat("1", 4097))
	write("memory.current", "bad")
	write("cpu.max", "1 0")
	r = NodeReading{}
	readCgroupV2(dir, at, &r)
	if r.CgroupMemoryLimitBytes.Value != nil || r.CgroupMemoryCurrentBytes.Value != nil || r.CgroupCPUQuotaCores.Value != nil {
		t.Fatal("malformed cgroup invented capacity")
	}
	r = NodeReading{}
	readCgroupV2(filepath.Join(dir, "missing"), at, &r)
	if r.CgroupMemoryLimitBytes.Value != nil || r.CgroupCPUQuotaCores.Value != nil {
		t.Fatal("missing cgroup invented capacity")
	}
}
