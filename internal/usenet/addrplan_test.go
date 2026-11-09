package usenet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestAddrPlanRaceAndFamilyBlacklist(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	v6a := netip.MustParseAddr("2001:db8::1")
	v6b := netip.MustParseAddr("2001:db8::2")
	v4a := netip.MustParseAddr("192.0.2.10")
	v4b := netip.MustParseAddr("192.0.2.11")

	ap := newAddrPlan("news.example.com", 563, true)
	ap.now = func() time.Time { return now }
	ap.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{v6a, v6b, v4a, v4b}, nil
	}

	// Scenario 1: All candidates fail, with IPv6 failing and IPv4 also failing;
	// verify racing caps at <=6 and returns an error.
	// Scenario 2: IPv6 family fails completely while IPv4 is not yet tried or
	// when IPv6 fails in a mixed race where all fail -> test family blacklisting:
	// First, test racing where v6a/v6b fail immediately and v4b wins faster than v4a.
	ap.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		ip := netip.MustParseAddr(host)
		if ip.Is6() {
			return nil, errors.New("network unreachable")
		}
		if ip == v4a {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}

	conn, err := ap.dialContext(context.Background(), "tcp", "news.example.com:563")
	if err != nil {
		t.Fatalf("dialContext failed: %v", err)
	}
	_ = conn.Close()
	if got := ap.preferredIP(); got != v4b {
		t.Fatalf("preferredIP = %v, want %v", got, v4b)
	}

	// Now simulate all candidates failing on a fresh plan where v6 fails and v4 fails,
	// and verify that when only v6 fails in single-family or mixed failure, blacklisting works.
	ap2 := newAddrPlan("news2.example.com", 563, true)
	ap2.now = func() time.Time { return now }
	ap2.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{v6a, v4a}, nil
	}
	// Make only v6a fail when dialed alone via noteFamilyFailure.
	ap2.noteFamilyFailure([]netip.Addr{v6a}) // pre-populate addrs first
	_, _ = ap2.candidates(context.Background())
	ap2.noteFamilyFailure([]netip.Addr{v6a})

	cands, err := ap2.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) != 1 || cands[0] != v4a {
		t.Fatalf("candidates after v6 blacklist = %v, want [%v]", cands, v4a)
	}

	// If v4 also gets blacklisted, both blacklists reset so we never brick the pool.
	ap2.noteFamilyFailure([]netip.Addr{v4a})
	cands, err = ap2.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates after both families blacklisted: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates after both blacklisted = %v, want 2 addresses", cands)
	}
}

func TestAddrPlanBodyRateRepinning(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	ipSlow := netip.MustParseAddr("192.0.2.1")
	ipFast := netip.MustParseAddr("192.0.2.2")

	ap := newAddrPlan("news.example.com", 563, true)
	ap.now = func() time.Time { return now }
	ap.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{ipSlow, ipFast}, nil
	}
	_, _ = ap.candidates(context.Background())

	// Seed 4 warm transfers on ipSlow at 1 MiB/s.
	for i := 0; i < addrRepinMinSamples; i++ {
		if ap.noteBodyRate(ipSlow, 256<<10, 256*time.Millisecond) {
			t.Fatalf("unexpected repin on initial preferred ipSlow sample %d", i)
		}
	}
	if got := ap.preferredIP(); got != ipSlow {
		t.Fatalf("preferredIP = %v, want %v", got, ipSlow)
	}

	// Transfers smaller than 64 KiB are ignored.
	if ap.noteBodyRate(ipFast, 16<<10, time.Millisecond) {
		t.Fatalf("unexpected repin on small transfer")
	}

	// 3 fast transfers on ipFast at 2 MiB/s (< 4 samples): no repin yet.
	for i := 0; i < addrRepinMinSamples-1; i++ {
		if ap.noteBodyRate(ipFast, 512<<10, 256*time.Millisecond) {
			t.Fatalf("repin triggered before min samples at i=%d", i)
		}
	}
	// 4th fast transfer (>=25% faster than ipSlow): repins to ipFast!
	if !ap.noteBodyRate(ipFast, 512<<10, 256*time.Millisecond) {
		t.Fatalf("expected repin to ipFast on 4th sample")
	}
	if got := ap.preferredIP(); got != ipFast {
		t.Fatalf("preferredIP = %v, want %v", got, ipFast)
	}

	// Candidates should now rank ipFast first.
	cands, err := ap.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) < 2 || cands[0] != ipFast {
		t.Fatalf("candidates order = %v, want %v first", cands, ipFast)
	}

	// Within the 30s anti-thrash cooldown, even if ipSlow suddenly bursts 4x faster, it cannot repin.
	for i := 0; i < addrRepinMinSamples*2; i++ {
		if ap.noteBodyRate(ipSlow, 2<<20, 100*time.Millisecond) {
			t.Fatalf("repin occurred inside 30s anti-thrash cooldown")
		}
	}
	if got := ap.preferredIP(); got != ipFast {
		t.Fatalf("preferredIP = %v, want %v during cooldown", got, ipFast)
	}

	// Advance past cooldown: now a sustained faster rate on ipSlow can repin.
	now = now.Add(addrRepinCooldown + time.Second)
	if !ap.noteBodyRate(ipSlow, 2<<20, 100*time.Millisecond) {
		t.Fatalf("expected repin after cooldown elapsed")
	}
	if got := ap.preferredIP(); got != ipSlow {
		t.Fatalf("preferredIP = %v, want %v after cooldown", got, ipSlow)
	}
}
