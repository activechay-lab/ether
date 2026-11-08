package debrid

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Keep a conservative shared ceiling across primary and hedge pools. TorBox
// recommends at most four connections, while other providers may impose
// host-specific limits. No operator tuning is needed.
const defaultHostConnections = 4

const resetCooldown = 3 * time.Minute

// connCap counts open connections per dial address and makes a dial wait,
// after asking the transports to drop idle connections, rather than exceed
// the limit.
type connCap struct {
	limit     int
	closeIdle func()
	log       *slog.Logger

	now func() time.Time

	mu       sync.Mutex
	open     map[string]int
	nextDial map[string]time.Time
	dialGap  time.Duration
	reduced  map[string]time.Time // addr -> end of its reduced-cap cooldown
	freed    chan struct{}        // closed and replaced whenever a connection closes
}

func newConnCap(limit int) *connCap {
	return &connCap{limit: limit, now: time.Now, open: map[string]int{}, nextDial: map[string]time.Time{}, reduced: map[string]time.Time{}, freed: make(chan struct{})}
}

// limitLocked is addr's effective cap and, while it is reduced, when the
// reduction ends.
func (c *connCap) limitLocked(addr string) (int, time.Time) {
	until, ok := c.reduced[addr]
	if !ok {
		return c.limit, time.Time{}
	}
	if !c.now().Before(until) {
		delete(c.reduced, addr)
		if c.log != nil {
			c.log.Info("media host connection cap restored", "host", addr, "limit", c.limit)
		}
		return c.limit, time.Time{}
	}
	return 1, until
}

// reduce lowers addr's cap for resetCooldown; further resets extend it.
// Open connections are left alone; new dials wait under the lower cap.
func (c *connCap) reduce(addr string) {
	if addr == "" {
		return
	}
	c.mu.Lock()
	_, already := c.reduced[addr]
	c.reduced[addr] = c.now().Add(resetCooldown)
	lim, _ := c.limitLocked(addr)
	c.mu.Unlock()
	if !already && c.log != nil {
		c.log.Warn("media host connection cap reduced", "host", addr, "limit", lim, "cooldown", resetCooldown)
	}
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (c *connCap) wrap(next dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := c.acquire(ctx, addr); err != nil {
			return nil, err
		}
		conn, err := next(ctx, network, addr)
		if err != nil {
			c.release(addr)
			return nil, err
		}
		return &cappedConn{Conn: conn, release: func() { c.release(addr) }}, nil
	}
}

func (c *connCap) acquire(ctx context.Context, addr string) error {
	asked := false
	began := time.Now()
	lim := c.limit
	defer func() {
		if wait := time.Since(began); wait > 250*time.Millisecond && c.log != nil {
			c.log.Info("media connection waited for the host cap", "host", addr, "wait", wait, "limit", lim)
		}
	}()
	for {
		c.mu.Lock()
		var until time.Time
		lim, until = c.limitLocked(addr)
		if c.open[addr] < lim {
			wait := c.nextDial[addr].Sub(c.now())
			if wait <= 0 {
				c.open[addr]++
				if c.dialGap > 0 {
					c.nextDial[addr] = c.now().Add(c.dialGap)
				}
				c.mu.Unlock()
				return nil
			}
			c.mu.Unlock()
			t := time.NewTimer(wait)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			}
			continue
		}
		freed := c.freed
		c.mu.Unlock()
		if !asked && c.closeIdle != nil {
			// Idle keep-alives are the cheapest connections to give back.
			asked = true
			go c.closeIdle()
		}
		var restored <-chan time.Time
		var t *time.Timer
		if !until.IsZero() {
			// The cap rises when the cooldown ends, even if nothing closes.
			t = time.NewTimer(until.Sub(c.now()))
			restored = t.C
		}
		select {
		case <-freed:
		case <-restored:
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			return ctx.Err()
		}
		if t != nil {
			t.Stop()
		}
	}
}

func (c *connCap) release(addr string) {
	c.mu.Lock()
	if c.open[addr] <= 1 {
		delete(c.open, addr)
	} else {
		c.open[addr]--
	}
	close(c.freed)
	c.freed = make(chan struct{})
	c.mu.Unlock()
}

// Reserve headroom for real reads, including connections in both pools.
func (c *connCap) hedgeAllowed(addr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit, _ := c.limitLocked(addr)
	return limit > 1 && c.open[addr] < limit-1
}

type cappedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *cappedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// current is addr's effective cap and, while it is reduced, when the
// reduction ends.
func (c *connCap) current(addr string) (int, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitLocked(addr)
}

// hostGate admits media requests to a host up to its connection cap, held
// until the response body is closed. The dial cap alone cannot order
// waiters: the transport hands a connection that turns idle to whichever
// request asked first, so a queued read-ahead could take it ahead of the
// playback read that is stalling a viewer. The gate decides instead: a
// waiting urgent read goes before every queued read-ahead and prefetch,
// and requests of equal priority keep their order.
type hostGate struct {
	limit func(addr string) (int, time.Time)
	now   func() time.Time
	log   *slog.Logger

	mu      sync.Mutex
	busy    map[string]int
	waiters map[string][]*gateWaiter
}

type gateWaiter struct {
	urgent  func() bool // evaluated at grant time: a warm-up can turn into Play
	ready   chan struct{}
	granted bool
}

func newHostGate(c *connCap) *hostGate {
	return &hostGate{limit: c.current, now: func() time.Time { return c.now() }, busy: map[string]int{}, waiters: map[string][]*gateWaiter{}}
}

// acquire waits for a request slot to addr. urgent reports whether a viewer
// is waiting on this request; it is consulted again whenever a slot frees.
func (g *hostGate) acquire(ctx context.Context, addr string, urgent func() bool) (func(), error) {
	if g == nil || addr == "" {
		return func() {}, nil
	}
	began := time.Now()
	g.mu.Lock()
	lim, until := g.limit(addr)
	if g.busy[addr] < lim && (len(g.waiters[addr]) == 0 || urgent()) {
		g.busy[addr]++
		g.mu.Unlock()
		return g.releaser(addr), nil
	}
	w := &gateWaiter{urgent: urgent, ready: make(chan struct{})}
	g.waiters[addr] = append(g.waiters[addr], w)
	g.mu.Unlock()
	defer func() {
		if wait := time.Since(began); wait > 250*time.Millisecond && g.log != nil {
			g.log.Info("media request waited for the host cap", "host", addr, "wait", wait, "limit", lim, "urgent", urgent())
		}
	}()
	for {
		var restored <-chan time.Time
		var t *time.Timer
		if !until.IsZero() {
			// A reduced cap rises when its cooldown ends, even if nothing
			// releases a slot.
			t = time.NewTimer(max(0, until.Sub(g.now())))
			restored = t.C
		}
		select {
		case <-w.ready:
			if t != nil {
				t.Stop()
			}
			return g.releaser(addr), nil
		case <-restored:
			g.mu.Lock()
			g.dispatchLocked(addr)
			lim, until = g.limit(addr)
			g.mu.Unlock()
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			g.mu.Lock()
			if w.granted {
				g.mu.Unlock()
				g.release(addr)
			} else {
				q := g.waiters[addr]
				for i, o := range q {
					if o == w {
						g.waiters[addr] = append(q[:i:i], q[i+1:]...)
						break
					}
				}
				if len(g.waiters[addr]) == 0 {
					delete(g.waiters, addr)
				}
				g.mu.Unlock()
			}
			return nil, ctx.Err()
		}
	}
}

// tryAcquire takes a spare slot without queueing (speculative hedges).
func (g *hostGate) tryAcquire(addr string) (func(), bool) {
	if g == nil || addr == "" {
		return func() {}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	lim, _ := g.limit(addr)
	if g.busy[addr] >= lim || len(g.waiters[addr]) > 0 {
		return nil, false
	}
	g.busy[addr]++
	return g.releaser(addr), true
}

func (g *hostGate) releaser(addr string) func() {
	var once sync.Once
	return func() { once.Do(func() { g.release(addr) }) }
}

func (g *hostGate) release(addr string) {
	g.mu.Lock()
	if g.busy[addr] <= 1 {
		delete(g.busy, addr)
	} else {
		g.busy[addr]--
	}
	g.dispatchLocked(addr)
	g.mu.Unlock()
}

// dispatchLocked grants free slots: the oldest urgent waiter first, else
// the oldest waiter.
func (g *hostGate) dispatchLocked(addr string) {
	lim, _ := g.limit(addr)
	for g.busy[addr] < lim {
		q := g.waiters[addr]
		if len(q) == 0 {
			break
		}
		pick := 0
		for i, w := range q {
			if w.urgent() {
				pick = i
				break
			}
		}
		w := q[pick]
		q = append(q[:pick:pick], q[pick+1:]...)
		if len(q) == 0 {
			delete(g.waiters, addr)
		} else {
			g.waiters[addr] = q
		}
		g.busy[addr]++
		w.granted = true
		close(w.ready)
	}
}
