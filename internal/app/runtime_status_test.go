package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/hoststat"
	"github.com/braedonsaunders/ether/internal/placement"
)

type runtimeNodeSampler func(context.Context) (hoststat.NodeReading, error)

func (f runtimeNodeSampler) Sample(ctx context.Context) (hoststat.NodeReading, error) { return f(ctx) }

func TestSelfRuntimeSnapshotIdentityAbsenceAndRetainedSamples(t *testing.T) {
	self := cluster.Node{ID: "exact-self", Incarnation: "exact-boot"}
	if got := (&App{}).selfRuntimeSnapshot(self); got != nil {
		t.Fatal("standalone invented deployment status")
	}
	raw, err := json.Marshal(clusterStatus{})
	if err != nil || strings.Contains(string(raw), "selfRuntime") {
		t.Fatal("unknown runtime was not omitted")
	}
	p := &deploymentRuntime{}
	a := &App{deployment: p}
	if a.selfRuntimeSnapshot(cluster.Node{}) != nil {
		t.Fatal("snapshot without exact identity")
	}
	p.protocol.Store(1)
	p.started.Store(true)
	p.ready.Store(true)
	p.quotaOpen.Store(true)
	old := time.Now().Add(-time.Hour).UTC()
	p.quota.Store(&runtimeQuotaSnapshot{SampledAt: old, Status: placement.QuotaStatus{Capacity: 4096, Matches: true}})
	now := old
	value := uint64(42)
	p.telemetry, err = hoststat.NewNodeResources(hoststat.NodeOptions{Now: func() time.Time { return now }, Sampler: runtimeNodeSampler(func(context.Context) (hoststat.NodeReading, error) {
		return hoststat.NodeReading{ProcessRSSBytes: hoststat.Metric[uint64]{Value: &value, At: old, State: hoststat.Available}}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = p.telemetry.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for p.telemetry.Snapshot().ProcessRSSBytes.Value == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	now = old.Add(time.Hour) // The sampler has joined; cached samples must age.
	st := a.selfRuntimeSnapshot(self)
	if st.NodeID != self.ID || st.Boot != self.Incarnation || st.Protocol != 1 || !st.Started || !st.ReadyExisting || !st.AcceptingNew || st.Draining {
		t.Fatalf("wrong local status: %+v", st)
	}
	if st.RuntimeSampledAt.Before(now.Add(-time.Minute)) || st.Quota == nil || !st.Quota.SampledAt.Equal(old) {
		t.Fatal("response time refreshed retained quota time")
	}
	if st.Resources == nil || !st.Resources.ProcessRSSBytes.At.Equal(old) || st.Resources.ProcessRSSBytes.State != hoststat.Stale {
		t.Fatal("resource sample age was replaced by response time")
	}
	st.Quota.Status.Capacity = 1
	*st.Resources.ProcessRSSBytes.Value = 1
	if p.quota.Load().Status.Capacity != 4096 || *p.telemetry.Snapshot().ProcessRSSBytes.Value != 42 {
		t.Fatal("snapshot shares mutable retained data")
	}
	a.draining.Store(true)
	st = a.selfRuntimeSnapshot(self)
	if !st.ReadyExisting || !st.Draining || st.AcceptingNew {
		t.Fatal("drain confused reachability and admission")
	}
	p.quotaOpen.Store(false)
	a.draining.Store(false)
	if a.selfRuntimeSnapshot(self).AcceptingNew {
		t.Fatal("retained quota sample overrode current admission failure")
	}
}
