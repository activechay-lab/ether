package hoststat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryLimit(t *testing.T) {
	const gib = 1 << 30
	cases := []struct {
		name       string
		env        string
		cgroup     uint64
		ram        uint64
		wantLimit  int64
		wantSource string
	}{
		{"env wins over everything", "2GiB", 4 * gib, 8 * gib, 0, LimitSourceEnv},
		{"env off still wins", "off", 0, 8 * gib, 0, LimitSourceEnv},
		{"container limit 90 percent", "", 4 * gib, 8 * gib, int64(4*gib - 4*gib/10), LimitSourceCgroupV2},
		{"container limit beats ram share", "", 2 * gib, 128 * gib, int64(2*gib - 2*gib/10), LimitSourceCgroupV2},
		{"small container has no 1GiB floor", "", 512 << 20, 8 * gib, int64(512<<20 - (512<<20)/10), LimitSourceCgroupV2},
		{"unlimited sentinel ignored", "", 9223372036854771712, 8 * gib, 4 * gib, LimitSourceRAM},
		{"8GiB ram half", "", 0, 8 * gib, 4 * gib, LimitSourceRAM},
		{"128GiB ram half", "", 0, 128 * gib, 64 * gib, LimitSourceRAM},
		{"3GiB ram leaves 1GiB reserve binds at half", "", 0, 3 * gib, 3 * gib / 2, LimitSourceRAM},
		{"tiny ram floor", "", 0, 512 << 20, gib, LimitSourceRAM},
		{"1GiB ram floor", "", 0, gib, gib, LimitSourceRAM},
		{"unknown everything", "", 0, 0, 0, LimitSourceNone},
		{"blank env is not an override", "  ", 0, 8 * gib, 4 * gib, LimitSourceRAM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, source := MemoryLimit(tc.env, tc.cgroup, tc.ram)
			if limit != tc.wantLimit || source != tc.wantSource {
				t.Fatalf("MemoryLimit(%q,%d,%d) = %d,%q want %d,%q", tc.env, tc.cgroup, tc.ram, limit, source, tc.wantLimit, tc.wantSource)
			}
		})
	}
}

func TestParseCgroup(t *testing.T) {
	if _, ok := ParseCgroupV2("max\n"); ok {
		t.Fatal("v2 max must be unlimited")
	}
	if n, ok := ParseCgroupV2("2147483648\n"); !ok || n != 2147483648 {
		t.Fatalf("v2 value = %d,%v", n, ok)
	}
	if _, ok := ParseCgroupV1("9223372036854771712"); ok {
		t.Fatal("v1 sentinel must be unlimited")
	}
	if n, ok := ParseCgroupV1("1073741824"); !ok || n != 1073741824 {
		t.Fatalf("v1 value = %d,%v", n, ok)
	}
	for _, bad := range []string{"", "abc", "-1", "0"} {
		if _, ok := ParseCgroupV1(bad); ok {
			t.Fatalf("v1 %q must not parse", bad)
		}
	}
}

func TestCgroupLimitFiles(t *testing.T) {
	write := func(t *testing.T, root, rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("v2", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "sys/fs/cgroup/memory.max", "2147483648")
		if n, src := CgroupLimit(root); n != 2147483648 || src != LimitSourceCgroupV2 {
			t.Fatalf("= %d,%q", n, src)
		}
	})
	t.Run("v2 unlimited does not fall through to v1", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "sys/fs/cgroup/memory.max", "max")
		write(t, root, "sys/fs/cgroup/memory/memory.limit_in_bytes", "1073741824")
		if n, src := CgroupLimit(root); n != 0 || src != "" {
			t.Fatalf("= %d,%q", n, src)
		}
	})
	t.Run("v1", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "sys/fs/cgroup/memory/memory.limit_in_bytes", "1073741824")
		if n, src := CgroupLimit(root); n != 1073741824 || src != LimitSourceCgroupV1 {
			t.Fatalf("= %d,%q", n, src)
		}
	})
	t.Run("none", func(t *testing.T) {
		if n, src := CgroupLimit(t.TempDir()); n != 0 || src != "" {
			t.Fatalf("= %d,%q", n, src)
		}
	})
}

func TestParseMemTotal(t *testing.T) {
	got := ParseMemTotal("MemTotal:        8167848 kB\nMemFree:          123456 kB\n")
	if got != 8167848*1024 {
		t.Fatalf("= %d", got)
	}
	if ParseMemTotal("MemFree: 1 kB\n") != 0 {
		t.Fatal("missing MemTotal must be 0")
	}
}

// The guard raises an automatic limit only when GC load comes from a live
// heap pressed against the limit, and never past the ceiling.
func TestGuardDecision(t *testing.T) {
	const gib = int64(1) << 30
	prev := gcSample{gcCPU: 100, totalCPU: 1000}
	cases := []struct {
		name        string
		gc, total   float64
		live        int64
		limit, ceil int64
		next        int64
		thrashing   bool
	}{
		{"healthy GC", 102, 1240, 2 * gib, 3 * gib, 6 * gib, 0, false},
		{"churn below the limit", 190, 1240, 1 * gib, 3 * gib, 6 * gib, 0, false},
		{"thrash raises to twice the live heap", 300, 1240, 2800 << 20, 3 * gib, 8 * gib, 5600 << 20, true},
		{"raise capped at ceiling", 300, 1240, 2800 << 20, 3 * gib, 4 * gib, 4 * gib, true},
		{"already at ceiling", 300, 1240, 2800 << 20, 3 * gib, 3 * gib, 0, true},
		{"unknown ceiling only reports", 300, 1240, 2800 << 20, 3 * gib, 0, 0, true},
	}
	for _, c := range cases {
		next, thrashing := guardDecision(prev, gcSample{gcCPU: c.gc, totalCPU: c.total, live: uint64(c.live)}, c.limit, c.ceil)
		if next != c.next || thrashing != c.thrashing {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, next, thrashing, c.next, c.thrashing)
		}
	}
}
