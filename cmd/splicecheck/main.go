// splicecheck verifies local offline HLS artifacts. The JSON input has Segments
// and Options fields matching internal/splicecheck; paths must be local files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/braedonsaunders/ether/internal/splicecheck"
)

func main() {
	input := flag.String("input", "", "JSON file containing Segments and Options")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: splicecheck -input artifacts.json")
		os.Exit(2)
	}
	f, e := os.Open(*input)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	var req struct {
		Segments []splicecheck.Segment
		Options  splicecheck.Options
	}
	e = json.NewDecoder(f).Decode(&req)
	f.Close()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	r, e := splicecheck.Check(ctx, req.Segments, req.Options)
	json.NewEncoder(os.Stdout).Encode(r)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
