package transcode

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// On-demand encoders run below live outputs; live ones keep normal priority.
func TestBackgroundEncoderRunsBelowLiveOutputs(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	for _, background := range []bool{false, true} {
		p, err := (ExecRunner{}).Start(Command{Path: sleep, Args: []string{"5"}, Background: background})
		if err != nil {
			t.Fatal(err)
		}
		pid := p.(*execProcess).PID()
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		p.Stop()
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+2:]))
		nice, _ := strconv.Atoi(fields[16])
		want := 0
		if background {
			want = encoderBackgroundNice
		}
		if nice != want {
			t.Fatalf("background=%v encoder nice %d, want %d", background, nice, want)
		}
	}
}
