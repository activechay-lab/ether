package hoststat

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MetricState distinguishes an unknown reading from a measured zero. Unlimited
// means the configured cgroup explicitly has no local limit, not infinite host capacity.
type MetricState string

const (
	Unavailable     MetricState = "unavailable"
	Available       MetricState = "available"
	Stale           MetricState = "stale"
	Unlimited       MetricState = "unlimited"
	MaxScratchPaths             = 16
	MaxGPUDevices               = 16
)

// Metric retains the original observation time. Stale may have a nil Value
// when a formerly unlimited local constraint expires. Only Available values may be
// used for admission; Stale values are diagnostic, never free capacity.
type Metric[T any] struct {
	Value *T          `json:"value"`
	At    time.Time   `json:"at"`
	State MetricState `json:"state"`
}

// NodeReading separates measurement scopes. CPU percentages use total host CPU
// capacity as denominator; ProcessRSSBytes is this process only (no children).
// Host readings in a container describe the host view, not pod capacity.
type NodeReading struct {
	HostCPUPercent           Metric[float64] `json:"host_cpu_percent"`
	ProcessCPUPercent        Metric[float64] `json:"process_cpu_percent"`
	HostMemoryTotalBytes     Metric[uint64]  `json:"host_memory_total_bytes"`
	HostMemoryAvailableBytes Metric[uint64]  `json:"host_memory_available_bytes"`
	ProcessRSSBytes          Metric[uint64]  `json:"process_rss_bytes"`
	// Cgroup fields describe ONLY the explicitly configured cgroup-v2 directory.
	// Ancestor limits/cpusets are not resolved: these are not effective pod capacity.
	CgroupMemoryLimitBytes   Metric[uint64]  `json:"cgroup_memory_limit_bytes"`
	CgroupMemoryCurrentBytes Metric[uint64]  `json:"cgroup_memory_current_bytes"`
	CgroupCPUQuotaCores      Metric[float64] `json:"cgroup_cpu_quota_cores"`
}

type FilesystemCapacity struct {
	TotalBytes Metric[uint64] `json:"total_bytes"`
	// Available is space available to this user, not privileged free blocks.
	AvailableBytes Metric[uint64] `json:"available_bytes"`
}
type ScratchSnapshot struct {
	Path string `json:"path"`
	FilesystemCapacity
}

// GPUReading is optional provider evidence, not inferred from device presence.
// Providers must supply bounded device IDs and verified capability strings.
type GPUReading struct {
	Device             string          `json:"device"`
	Capabilities       []string        `json:"capabilities"`
	UtilizationPercent Metric[float64] `json:"utilization_percent"`
}
type NodeSnapshot struct {
	NodeReading
	Scratch  []ScratchSnapshot `json:"scratch"`
	GPU      []GPUReading      `json:"gpu"`
	GPUState MetricState       `json:"gpu_state"`
	GPUAt    time.Time         `json:"gpu_at"`
}

// NodeSampler and GPUProvider must honor ctx promptly and must not retain or
// mutate returned data. Run invokes providers serially (no spawned I/O goroutines).
// A provider that ignores context delays Run shutdown; it is never abandoned in
// a background goroutine. Snapshot never waits on a provider or performs I/O.
type NodeSampler interface {
	Sample(context.Context) (NodeReading, error)
}
type GPUProvider interface {
	SampleGPU(context.Context) ([]GPUReading, error)
}

// NodeOptions bounds retained data and sampling frequency. SampleTimeout is
// cooperative, not a syscall preemption guarantee: providers must honor context,
// and native filesystem sampling expects local paths. A hung remote mount may
// delay Run; cached Snapshot reads continue and age their observations.
type NodeOptions struct {
	Now           func() time.Time
	Interval      time.Duration // default 5s; minimum 1s
	MaxAge        time.Duration // default 3 intervals; at least one interval
	SampleTimeout time.Duration // default 1s; at most one interval
	ScratchPaths  []string      // explicit absolute paths; no recursive scans
	CgroupV2Path  string        // optional explicit absolute cgroup directory, Linux only
	Sampler       NodeSampler
	GPU           GPUProvider
	Filesystem    func(context.Context, string) (FilesystemCapacity, error)
}

// NodeResources holds exactly one snapshot, independently of Plex Resources.
// Construction does no I/O and starts no goroutines. The owner starts Run and
// cancels its context, then waits for Run to return before releasing providers.
type NodeResources struct {
	opts     NodeOptions
	mu       sync.RWMutex
	running  bool
	snapshot NodeSnapshot
}

func NewNodeResources(opts NodeOptions) (*NodeResources, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval == 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.MaxAge == 0 {
		opts.MaxAge = 3 * opts.Interval
	}
	if opts.SampleTimeout == 0 {
		opts.SampleTimeout = time.Second
	}
	if opts.Interval < time.Second || opts.MaxAge < opts.Interval || opts.SampleTimeout <= 0 || opts.SampleTimeout > opts.Interval {
		return nil, errors.New("invalid node sampling durations")
	}
	if len(opts.ScratchPaths) > MaxScratchPaths {
		return nil, errors.New("too many scratch paths")
	}
	opts.ScratchPaths = append([]string(nil), opts.ScratchPaths...)
	seen := make(map[string]bool)
	for i, p := range opts.ScratchPaths {
		if len(p) > 4096 || !filepath.IsAbs(p) {
			return nil, errors.New("scratch paths must be absolute")
		}
		p = filepath.Clean(p)
		if seen[p] {
			return nil, errors.New("duplicate scratch path")
		}
		seen[p] = true
		opts.ScratchPaths[i] = p
	}
	if opts.CgroupV2Path != "" && !filepath.IsAbs(opts.CgroupV2Path) {
		return nil, errors.New("cgroup path must be absolute")
	}
	if opts.Sampler == nil {
		opts.Sampler = newNativeNodeSampler(opts.Now, opts.CgroupV2Path)
	}
	if opts.Filesystem == nil {
		opts.Filesystem = nativeFilesystem
	}
	r := &NodeResources{opts: opts}
	r.snapshot.Scratch = make([]ScratchSnapshot, len(opts.ScratchPaths))
	for i, p := range opts.ScratchPaths {
		r.snapshot.Scratch[i].Path = p
	}
	return r, nil
}

// Run samples serially until cancellation. Callers own the goroutine and must
// wait for its return. Cancellation and SampleTimeout are cooperative; neither
// can preempt a native filesystem syscall or a provider that ignores context.
func (r *NodeResources) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return errors.New("node sampler already running")
	}
	r.running = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.collect(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (r *NodeResources) collect(ctx context.Context) {
	sampleCtx, cancel := context.WithTimeout(ctx, r.opts.SampleTimeout)
	defer cancel()
	s := NodeSnapshot{GPUState: Unavailable}
	reading, err := r.opts.Sampler.Sample(sampleCtx)
	if err == nil && sampleCtx.Err() == nil {
		s.NodeReading = reading
	}
	for _, p := range r.opts.ScratchPaths {
		f := ScratchSnapshot{Path: p}
		if sampleCtx.Err() == nil {
			capacity, err := r.opts.Filesystem(sampleCtx, p)
			if err == nil && sampleCtx.Err() == nil {
				f.FilesystemCapacity = capacity
			}
		}
		s.Scratch = append(s.Scratch, f)
	}
	if r.opts.GPU != nil && sampleCtx.Err() == nil {
		devices, err := r.opts.GPU.SampleGPU(sampleCtx)
		if err == nil && sampleCtx.Err() == nil {
			if len(devices) > MaxGPUDevices {
				devices = devices[:MaxGPUDevices]
			}
			s.GPU = devices
			s.GPUState = Available
			s.GPUAt = r.opts.Now()
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Clone outside the lock; slow providers cannot stall cached readers.
	s = cloneNodeSnapshot(s, r.opts.Now(), r.opts.MaxAge)
	r.mu.Lock()
	r.snapshot = s
	r.mu.Unlock()
}
func (r *NodeResources) Snapshot() NodeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneNodeSnapshot(r.snapshot, r.opts.Now(), r.opts.MaxAge)
}
func observed[T any](v T, at time.Time) Metric[T] {
	return Metric[T]{Value: &v, At: at, State: Available}
}
func cloneMetric[T any](m Metric[T], now time.Time, age time.Duration) Metric[T] {
	if m.At.IsZero() || m.At.After(now) || (m.State != Available && m.State != Unlimited && m.State != Stale) || (m.State == Available && m.Value == nil) {
		return Metric[T]{State: Unavailable}
	}
	if m.Value != nil {
		if v, ok := any(*m.Value).(float64); ok && (math.IsNaN(v) || math.IsInf(v, 0) || v < 0) {
			return Metric[T]{State: Unavailable}
		}
		v := *m.Value
		m.Value = &v
	}
	if now.Sub(m.At) > age {
		m.State = Stale
	}
	return m
}
func cloneNodeSnapshot(s NodeSnapshot, now time.Time, age time.Duration) NodeSnapshot {
	s.HostCPUPercent = cloneMetric(s.HostCPUPercent, now, age)
	s.ProcessCPUPercent = cloneMetric(s.ProcessCPUPercent, now, age)
	s.HostMemoryTotalBytes = cloneMetric(s.HostMemoryTotalBytes, now, age)
	s.HostMemoryAvailableBytes = cloneMetric(s.HostMemoryAvailableBytes, now, age)
	s.ProcessRSSBytes = cloneMetric(s.ProcessRSSBytes, now, age)
	s.CgroupMemoryLimitBytes = cloneMetric(s.CgroupMemoryLimitBytes, now, age)
	s.CgroupMemoryCurrentBytes = cloneMetric(s.CgroupMemoryCurrentBytes, now, age)
	s.CgroupCPUQuotaCores = cloneMetric(s.CgroupCPUQuotaCores, now, age)
	s.Scratch = append([]ScratchSnapshot(nil), s.Scratch...)
	for i := range s.Scratch {
		s.Scratch[i].TotalBytes = cloneMetric(s.Scratch[i].TotalBytes, now, age)
		s.Scratch[i].AvailableBytes = cloneMetric(s.Scratch[i].AvailableBytes, now, age)
	}
	s.GPU = append([]GPUReading(nil), s.GPU...)
	for i := range s.GPU {
		g := &s.GPU[i]
		if len(g.Device) > 256 {
			g.Device = g.Device[:256]
		}
		g.Device = strings.Clone(g.Device)
		if len(g.Capabilities) > 64 {
			g.Capabilities = g.Capabilities[:64]
		}
		g.Capabilities = append([]string(nil), g.Capabilities...)
		for j, c := range g.Capabilities {
			if len(c) > 128 {
				c = c[:128]
			}
			g.Capabilities[j] = strings.Clone(c)
		}
		g.UtilizationPercent = cloneMetric(g.UtilizationPercent, now, age)
	}
	if s.GPUState == "" {
		s.GPUState = Unavailable
	}
	if s.GPUState == Available && (s.GPUAt.IsZero() || s.GPUAt.After(now)) {
		s.GPUState = Unavailable
	}
	if s.GPUState == Available && now.Sub(s.GPUAt) > age {
		s.GPUState = Stale
	}
	return s
}
