package usenet

import (
	"cmp"
	"context"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	addrRefreshTTL        = 5 * time.Minute
	addrMaxRaceCandidates = 6
	addrRepinCooldown     = 30 * time.Second
	addrRepinMinSamples   = 4
	addrRepinMinBytes     = 64 << 10
	addrRepinBeatRatio    = 1.25
)

type addrRateStat struct {
	ewmaBps float64
	samples int
}

// addrPlan manages multi-address DNS resolution, family-level (IPv4/IPv6)
// reachability blacklisting, parallel TCP dial racing across up to 6
// resolved IPs, and warm BODY throughput repinning for one provider pool.
type addrPlan struct {
	host   string
	port   int
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	now    func() time.Time

	mu          sync.Mutex
	addrs       []netip.Addr
	preferred   netip.Addr
	resolvedAt  time.Time
	v4DeadUntil time.Time
	v6DeadUntil time.Time
	rates       map[netip.Addr]addrRateStat
	lastRepin   time.Time
}

func newAddrPlan(host string, port int, tls bool) *addrPlan {
	if port == 0 {
		port = 119
		if tls {
			port = 563
		}
	}
	d := &net.Dialer{KeepAlive: 30 * time.Second}
	return &addrPlan{
		host: host,
		port: port,
		lookup: func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		},
		dial:  d.DialContext,
		now:   time.Now,
		rates: make(map[netip.Addr]addrRateStat),
	}
}

func (ap *addrPlan) candidates(ctx context.Context) ([]netip.Addr, error) {
	now := ap.now()
	ap.mu.Lock()
	stale := len(ap.addrs) == 0 || now.Sub(ap.resolvedAt) >= addrRefreshTTL
	ap.mu.Unlock()

	if stale {
		if ip, err := netip.ParseAddr(ap.host); err == nil {
			ap.mu.Lock()
			ap.addrs = []netip.Addr{ip.Unmap()}
			ap.resolvedAt = now
			if !ap.preferred.IsValid() {
				ap.preferred = ip.Unmap()
			}
			ap.mu.Unlock()
		} else {
			ips, err := ap.lookup(ctx, ap.host)
			if err != nil {
				ap.mu.Lock()
				cached := slices.Clone(ap.addrs)
				ap.mu.Unlock()
				if len(cached) > 0 {
					return cached, nil
				}
				return nil, err
			}
			var clean []netip.Addr
			for _, ip := range ips {
				ip = ip.Unmap()
				if ip.IsValid() && !slices.Contains(clean, ip) {
					clean = append(clean, ip)
				}
			}
			ap.mu.Lock()
			if len(clean) > 0 {
				ap.addrs = clean
				ap.resolvedAt = now
				if ap.preferred.IsValid() && !slices.Contains(clean, ap.preferred) {
					ap.preferred = netip.Addr{}
				}
			}
			ap.mu.Unlock()
		}
	}

	ap.mu.Lock()
	defer ap.mu.Unlock()
	if len(ap.addrs) == 0 {
		return nil, &net.DNSError{Err: "no addresses found", Name: ap.host, IsNotFound: true}
	}
	v4OK := now.After(ap.v4DeadUntil)
	v6OK := now.After(ap.v6DeadUntil)
	var out []netip.Addr
	for _, ip := range ap.addrs {
		if (ip.Is4() && v4OK) || (ip.Is6() && v6OK) {
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		// Both families marked dead: clear family blacklists and try all.
		ap.v4DeadUntil = time.Time{}
		ap.v6DeadUntil = time.Time{}
		out = slices.Clone(ap.addrs)
	}
	pref := ap.preferred
	rates := ap.rates
	slices.SortStableFunc(out, func(a, b netip.Addr) int {
		if (a == pref) != (b == pref) {
			if a == pref {
				return -1
			}
			return 1
		}
		return cmp.Compare(rates[b].ewmaBps, rates[a].ewmaBps)
	})
	if len(out) > addrMaxRaceCandidates {
		out = out[:addrMaxRaceCandidates]
	}
	return out, nil
}

// dialContext connects to the provider. When a single candidate exists, it
// dials directly; with multiple candidates, it races up to 6 in parallel,
// returning the first TCP connection to complete and cancelling the losers.
func (ap *addrPlan) dialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	cands, err := ap.candidates(ctx)
	if err != nil {
		return nil, err
	}
	portStr := strconv.Itoa(ap.port)
	if len(cands) == 1 {
		c, derr := ap.dial(ctx, network, net.JoinHostPort(cands[0].String(), portStr))
		if derr != nil {
			ap.noteFamilyFailure(cands)
		}
		return c, derr
	}
	return ap.race(ctx, network, cands, portStr)
}

func (ap *addrPlan) race(ctx context.Context, network string, cands []netip.Addr, portStr string) (net.Conn, error) {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type res struct {
		ip   netip.Addr
		conn net.Conn
		err  error
	}
	ch := make(chan res, len(cands))
	for _, ip := range cands {
		ip := ip
		go func() {
			c, err := ap.dial(rctx, network, net.JoinHostPort(ip.String(), portStr))
			ch <- res{ip: ip, conn: c, err: err}
		}()
	}

	var (
		lastErr error
		v4Fails int
		v6Fails int
		v4Tried int
		v6Tried int
	)
	for _, ip := range cands {
		if ip.Is4() {
			v4Tried++
		} else {
			v6Tried++
		}
	}
	for i := 0; i < len(cands); i++ {
		r := <-ch
		if r.err == nil && r.conn != nil {
			cancel()
			go func(rem int) {
				for j := 0; j < rem; j++ {
					late := <-ch
					if late.err == nil && late.conn != nil {
						_ = late.conn.Close()
					}
				}
			}(len(cands) - 1 - i)
			ap.mu.Lock()
			if !ap.preferred.IsValid() {
				ap.preferred = r.ip
			}
			ap.mu.Unlock()
			return r.conn, nil
		}
		lastErr = r.err
		if r.ip.Is4() {
			v4Fails++
		} else {
			v6Fails++
		}
	}
	now := ap.now()
	ap.mu.Lock()
	if v4Tried > 0 && v4Fails == v4Tried && v6Tried > 0 {
		ap.v4DeadUntil = now.Add(addrRefreshTTL)
	}
	if v6Tried > 0 && v6Fails == v6Tried && v4Tried > 0 {
		ap.v6DeadUntil = now.Add(addrRefreshTTL)
	}
	ap.mu.Unlock()
	return nil, lastErr
}

func (ap *addrPlan) noteFamilyFailure(cands []netip.Addr) {
	now := ap.now()
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if len(ap.addrs) <= 1 {
		return
	}
	allV4, allV6 := true, true
	for _, ip := range cands {
		if ip.Is4() {
			allV6 = false
		} else {
			allV4 = false
		}
	}
	if allV4 {
		ap.v4DeadUntil = now.Add(addrRefreshTTL)
	} else if allV6 {
		ap.v6DeadUntil = now.Add(addrRefreshTTL)
	}
}

// noteBodyRate records a warm BODY transfer rate for ip and reports whether
// ip has sustained >=25% higher throughput than the current preferred IP over
// at least addrRepinMinSamples transfers (with a 30s anti-thrash cooldown).
// When repinned, idle connections on slower IPs can be gently retired as they
// cycle through the pool without interrupting active transfers.
func (ap *addrPlan) noteBodyRate(ip netip.Addr, bytes int64, d time.Duration) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || bytes < addrRepinMinBytes || d <= 0 {
		return false
	}
	bps := float64(bytes) / d.Seconds()
	const alpha = 0.2
	now := ap.now()

	ap.mu.Lock()
	defer ap.mu.Unlock()
	st := ap.rates[ip]
	if st.samples == 0 {
		st.ewmaBps = bps
	} else {
		st.ewmaBps += alpha * (bps - st.ewmaBps)
	}
	st.samples++
	ap.rates[ip] = st

	if !ap.preferred.IsValid() {
		ap.preferred = ip
		return false
	}
	if ip == ap.preferred || st.samples < addrRepinMinSamples || now.Sub(ap.lastRepin) < addrRepinCooldown {
		return false
	}
	prefStat := ap.rates[ap.preferred]
	if prefStat.samples >= addrRepinMinSamples && st.ewmaBps >= prefStat.ewmaBps*addrRepinBeatRatio {
		ap.preferred = ip
		ap.lastRepin = now
		return true
	}
	return false
}

func (ap *addrPlan) preferredIP() netip.Addr {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.preferred
}
