package raster

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type point struct{ x, y float64 }
type contour struct {
	p      []point
	closed bool
}

var pathToken = regexp.MustCompile(`[A-Za-z]|[-+]?(?:\d*\.\d+|\d+\.?\d*)(?:[eE][-+]?\d+)?`)

// parsePath flattens all SVG path commands into deterministic contours.
func parsePath(s string) ([]contour, error) {
	ts := pathToken.FindAllString(s, -1)
	var out []contour
	var c contour
	var pos, start, last point
	cmd := ""
	prev := ""
	i := 0
	flush := func() {
		if len(c.p) > 0 {
			out = append(out, c)
			c = contour{}
		}
	}
	num := func() (float64, error) {
		if i >= len(ts) {
			return 0, fmt.Errorf("short path")
		}
		n, e := strconv.ParseFloat(ts[i], 64)
		i++
		return n, e
	}
	for i < len(ts) {
		if strings.ContainsAny(ts[i], "MmLlHhVvCcSsQqTtAaZz") {
			cmd = ts[i]
			i++
		} else if cmd == "" {
			return nil, fmt.Errorf("invalid path command")
		}
		rel := cmd == strings.ToLower(cmd)
		op := strings.ToUpper(cmd)
		if op == "Z" {
			c.closed = true
			flush()
			pos = start
			cmd = ""
			prev = op
			continue
		}
		n := map[string]int{"M": 2, "L": 2, "H": 1, "V": 1, "C": 6, "S": 4, "Q": 4, "T": 2, "A": 7}[op]
		if n == 0 {
			return nil, fmt.Errorf("unknown command")
		}
		v := make([]float64, n)
		for j := range v {
			f, e := num()
			if e != nil {
				return nil, e
			}
			v[j] = f
		}
		xy := func(j int) point {
			p := point{v[j], v[j+1]}
			if rel {
				p.x += pos.x
				p.y += pos.y
			}
			return p
		}
		add := func(p point) { c.p = append(c.p, p) }
		switch op {
		case "M":
			flush()
			pos = xy(0)
			start = pos
			add(pos)
			if rel {
				cmd = "l"
			} else {
				cmd = "L"
			}
		case "L":
			pos = xy(0)
			add(pos)
		case "H":
			x := v[0]
			if rel {
				x += pos.x
			}
			pos.x = x
			add(pos)
		case "V":
			y := v[0]
			if rel {
				y += pos.y
			}
			pos.y = y
			add(pos)
		case "Q", "T":
			a := point{2*pos.x - last.x, 2*pos.y - last.y}
			end := xy(0)
			if op == "Q" {
				a = xy(0)
				end = xy(2)
			} else if prev != "Q" && prev != "T" {
				a = pos
			}
			p := pos
			for j := 1; j <= 24; j++ {
				t := float64(j) / 24
				u := 1 - t
				add(point{u*u*p.x + 2*u*t*a.x + t*t*end.x, u*u*p.y + 2*u*t*a.y + t*t*end.y})
			}
			last = a
			pos = end
		case "C", "S":
			a := point{2*pos.x - last.x, 2*pos.y - last.y}
			b, end := xy(0), xy(2)
			if op == "C" {
				a = xy(0)
				b = xy(2)
				end = xy(4)
			} else if prev != "C" && prev != "S" {
				a = pos
			}
			p := pos
			for j := 1; j <= 32; j++ {
				t := float64(j) / 32
				u := 1 - t
				add(point{u*u*u*p.x + 3*u*u*t*a.x + 3*u*t*t*b.x + t*t*t*end.x, u*u*u*p.y + 3*u*u*t*a.y + 3*u*t*t*b.y + t*t*t*end.y})
			}
			last = b
			pos = end
		case "A":
			end := xy(5)
			for _, p := range arc(pos, end, v[0], v[1], v[2], v[3] != 0, v[4] != 0) {
				add(p)
			}
			pos = end
		}
		prev = op
	}
	flush()
	return out, nil
}
func arc(p, q point, rx, ry, angle float64, large, sweep bool) []point {
	rx = math.Abs(rx)
	ry = math.Abs(ry)
	if rx == 0 || ry == 0 {
		return []point{q}
	}
	if p == q {
		return nil
	}
	phi := angle * math.Pi / 180
	co, si := math.Cos(phi), math.Sin(phi)
	dx, dy := (p.x-q.x)/2, (p.y-q.y)/2
	x, y := co*dx+si*dy, -si*dx+co*dy
	k := x*x/(rx*rx) + y*y/(ry*ry)
	if k > 1 {
		rx *= math.Sqrt(k)
		ry *= math.Sqrt(k)
	}
	f := math.Sqrt(max(0, (rx*rx*ry*ry-rx*rx*y*y-ry*ry*x*x)/(rx*rx*y*y+ry*ry*x*x)))
	if large == sweep {
		f = -f
	}
	cx, cy := f*rx*y/ry, -f*ry*x/rx
	ox, oy := co*cx-si*cy+(p.x+q.x)/2, si*cx+co*cy+(p.y+q.y)/2
	a := math.Atan2((y-cy)/ry, (x-cx)/rx)
	b := math.Atan2((-y-cy)/ry, (-x-cx)/rx)
	d := b - a
	if sweep && d < 0 {
		d += 2 * math.Pi
	}
	if !sweep && d > 0 {
		d -= 2 * math.Pi
	}
	n := max(1, int(math.Ceil(math.Abs(d)*16)))
	out := make([]point, 0, n)
	for j := 1; j <= n; j++ {
		v := a + d*float64(j)/float64(n)
		x, y := rx*math.Cos(v), ry*math.Sin(v)
		out = append(out, point{co*x - si*y + ox, si*x + co*y + oy})
	}
	return out
}
