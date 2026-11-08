package perfharness

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// BaselineRow is one step's measurements against one server: its cold
// request (the first a freshly restarted server answered), warm latency
// percentiles, response sizes, and the server's CPU time and block IO per
// request. It is what etherperf baseline writes, for PMS and for Ether
// alike, and what etherperf compare reads.
type BaselineRow struct {
	Client string `json:"client"`
	Step   string `json:"step"`
	Class  string `json:"class"`
	// Status is the HTTP status of the warm requests (of the cold one when
	// there were none); 0 when the step was skipped or failed to connect.
	Status  int    `json:"status"`
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`

	Cold      time.Duration `json:"cold"`
	ColdCPU   time.Duration `json:"coldCpu"`
	ColdRead  int64         `json:"coldReadBytes"`
	ColdWrite int64         `json:"coldWriteBytes"`

	WarmN     int           `json:"warmN"`
	WarmP50   time.Duration `json:"warmP50"`
	WarmP95   time.Duration `json:"warmP95"`
	WarmCPU   time.Duration `json:"warmCpuMedian"`
	WarmRead  int64         `json:"warmReadBytesMedian"`
	WarmWrite int64         `json:"warmWriteBytesMedian"`

	Bytes     int `json:"bytes"`
	GzipBytes int `json:"gzipBytes"`
	WireBytes int `json:"wireBytes"`
	Items     int `json:"items"`
	TotalSize int `json:"totalSize"`

	// Queries is the median statement count per request, when the server
	// wrote a perf trace (Ether only).
	Queries int64 `json:"queries,omitempty"`
}

// SummarizeBaseline groups a measured replay's results by client and step.
// Only the cold and warm phases count; others (discovery, priming) do not.
func SummarizeBaseline(results []Result, trace map[string]TraceRecord, order []Client) []BaselineRow {
	type acc struct {
		row             BaselineRow
		warm, cpu       []time.Duration
		read, write     []int64
		bytes, gz, wire []int64
		queries         []int64
		coldSeen        bool
	}
	byKey := map[string]*acc{}
	var keys []string
	for _, c := range order {
		for _, st := range c.Steps {
			k := c.Name + "\x00" + st.Name
			if byKey[k] == nil {
				byKey[k] = &acc{row: BaselineRow{Client: c.Name, Step: st.Name, Class: st.Class}}
				keys = append(keys, k)
			}
		}
	}
	for _, res := range results {
		a := byKey[res.Client+"\x00"+res.Step]
		if a == nil || (res.Phase != "cold" && res.Phase != "warm") {
			continue
		}
		if res.Skipped != "" {
			a.row.Skipped = res.Skipped
			continue
		}
		if res.Status == 0 {
			a.row.Error = res.Err
			continue
		}
		if tr, ok := trace[res.Tag]; ok {
			a.queries = append(a.queries, tr.Queries)
		}
		if res.Phase == "cold" {
			if !a.coldSeen {
				a.coldSeen = true
				a.row.Cold, a.row.ColdCPU, a.row.ColdRead, a.row.ColdWrite = res.Dur, res.CPU, res.ReadBytes, res.WriteBytes
				if a.row.Status == 0 {
					a.row.Status = res.Status
				}
			}
			continue
		}
		a.row.Status = res.Status
		if res.Err != "" && res.Status >= 400 {
			a.row.Error = res.Err
		}
		a.warm = append(a.warm, res.Dur)
		a.cpu = append(a.cpu, res.CPU)
		a.read = append(a.read, res.ReadBytes)
		a.write = append(a.write, res.WriteBytes)
		a.bytes = append(a.bytes, int64(res.Bytes))
		a.gz = append(a.gz, int64(res.GzipBytes))
		a.wire = append(a.wire, int64(res.WireBytes))
		a.row.Items, a.row.TotalSize = res.Items, res.TotalSize
	}
	out := make([]BaselineRow, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		r := a.row
		r.WarmN = len(a.warm)
		r.WarmP50, r.WarmP95 = percentile(a.warm, 0.5), percentile(a.warm, 0.95)
		r.WarmCPU = percentile(a.cpu, 0.5)
		r.WarmRead, r.WarmWrite = median64(a.read), median64(a.write)
		r.Bytes, r.GzipBytes, r.WireBytes = int(median64(a.bytes)), int(median64(a.gz)), int(median64(a.wire))
		r.Queries = median64(a.queries)
		out = append(out, r)
	}
	return out
}

// WriteBaselineReport writes one server's measured replay as markdown.
func WriteBaselineReport(w io.Writer, title string, meta []string, rows []BaselineRow) {
	fmt.Fprintf(w, "# %s\n\n", title)
	for _, m := range meta {
		fmt.Fprintf(w, "- %s\n", m)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cold is the first request after a restart with the server's files evicted from the page cache. CPU is the server's whole cgroup (user + system) during the request; read and write are block-device bytes (page-cache hits are free; writes are charged at writeback). Bytes are the decompressed body; gzip is that body gzipped at the default level; wire is what the server sent to a client asking for gzip.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| client | step | status | items | cold | cold CPU | cold read | warm p50 | warm p95 | warm CPU p50 | bytes | gzip | wire | queries |")
	fmt.Fprintln(w, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, r := range rows {
		status := fmt.Sprint(r.Status)
		switch {
		case r.Skipped != "":
			status = "skip " + r.Skipped
		case r.Status == 0:
			status = "error " + oneLine(r.Error)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			r.Client, strings.ReplaceAll(r.Step, "|", "/"), status, items(r), ms(r.Cold), cpuMS(r.ColdCPU, r.Cold), kb(r.ColdRead),
			ms(r.WarmP50), ms(r.WarmP95), cpuMS(r.WarmCPU, r.WarmP50), kb(int64(r.Bytes)), kb(int64(r.GzipBytes)), kb(int64(r.WireBytes)), queries(r.Queries))
	}
}

// Target is one server's baseline rows under a label, for WriteComparison.
type Target struct {
	Label string
	Rows  []BaselineRow
}

// WriteComparison writes per-step tables comparing targets: latency, CPU
// and response size. Steps are matched by client and step name.
func WriteComparison(w io.Writer, targets []Target, client string) {
	type key struct{ client, step string }
	var order []key
	seen := map[key]bool{}
	index := make([]map[key]BaselineRow, len(targets))
	for i, t := range targets {
		index[i] = map[key]BaselineRow{}
		for _, r := range t.Rows {
			if client != "" && r.Client != client {
				continue
			}
			k := key{r.Client, r.Step}
			index[i][k] = r
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	header := func(title string) {
		fmt.Fprintf(w, "### %s\n\n| step |", title)
		for _, t := range targets {
			fmt.Fprintf(w, " %s |", t.Label)
		}
		fmt.Fprintf(w, "\n|---|")
		for range targets {
			fmt.Fprint(w, "---:|")
		}
		fmt.Fprintf(w, "\n")
	}
	table := func(title string, f func(BaselineRow) string) {
		header(title)
		for _, k := range order {
			name := k.step
			if client == "" {
				name = k.client + " " + name
			}
			fmt.Fprintf(w, "| %s |", strings.ReplaceAll(name, "|", "/"))
			for i := range targets {
				r, ok := index[i][k]
				switch {
				case !ok:
					fmt.Fprint(w, " – |")
				case r.Skipped != "":
					fmt.Fprint(w, " skipped |")
				case r.Status == 0:
					fmt.Fprint(w, " error |")
				case r.Status >= 400:
					fmt.Fprintf(w, " %d |", r.Status)
				default:
					fmt.Fprintf(w, " %s |", f(r))
				}
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}
	table("Warm latency, p50 / p95", func(r BaselineRow) string { return ms(r.WarmP50) + " / " + ms(r.WarmP95) })
	table("Cold latency (first request after restart, page cache evicted)", func(r BaselineRow) string { return ms(r.Cold) })
	table("Server CPU per request, warm median / cold", func(r BaselineRow) string { return cpuMS(r.WarmCPU, r.WarmP50) + " / " + cpuMS(r.ColdCPU, r.Cold) })
	table("Cold block reads per request", func(r BaselineRow) string { return kb(r.ColdRead) })
	table("Response: items (of total) · bytes · gzip", func(r BaselineRow) string {
		return items(r) + " · " + kb(int64(r.Bytes)) + " · " + kb(int64(r.GzipBytes))
	})
}

func items(r BaselineRow) string {
	if r.TotalSize > 0 {
		return fmt.Sprintf("%d/%d", r.Items, r.TotalSize)
	}
	return fmt.Sprint(r.Items)
}

// cpuMS prints a CPU time; "0" when the probe saw none while the request
// itself took time (cgroup accounting is charged at scheduler ticks for
// very short bursts).
func cpuMS(d, wall time.Duration) string {
	if d <= 0 {
		if wall > 0 {
			return "0"
		}
		return "–"
	}
	return ms(d)
}

func kb(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 10<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}

func queries(n int64) string {
	if n == 0 {
		return "–"
	}
	return fmt.Sprint(n)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", "/")
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}
