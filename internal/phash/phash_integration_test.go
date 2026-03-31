//go:build integration

package phash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestComputeIntegration(t *testing.T) {
	requireMediaTools(t)

	tmp := t.TempDir()
	video := filepath.Join(tmp, "sample.mp4")

	gen := exec.Command(
		"ffmpeg",
		"-y",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=4:size=320x240:rate=30",
		"-pix_fmt", "yuv420p",
		video,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate sample video: %v (%s)", err, string(out))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	got, err := Compute(ctx, video)
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if got.ResolutionX != 320 || got.ResolutionY != 240 {
		t.Fatalf("unexpected resolution: %dx%d", got.ResolutionX, got.ResolutionY)
	}
	if got.Duration <= 0 {
		t.Fatalf("duration should be > 0, got %f", got.Duration)
	}
	if got.Bitrate <= 0 {
		t.Fatalf("bitrate should be > 0, got %f", got.Bitrate)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(got.PHash) {
		t.Fatalf("phash format unexpected: %q", got.PHash)
	}

	if _, err := os.Stat(video); err != nil {
		t.Fatalf("sample video missing: %v", err)
	}
}

func TestComputeIntegrationFixture(t *testing.T) {
	requireMediaTools(t)

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "tests", "tests__LosAlamosPhysicalSimulations_1m.mp4"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	if _, err := os.Stat(fixturePath); err != nil {
		t.Skipf("fixture not found: %s (%v)", fixturePath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	got, err := Compute(ctx, fixturePath)
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	want := Result{
		PHash:       "f27fc7482aba5094",
		ResolutionX: 720,
		ResolutionY: 486,
		Duration:    60.0,
		Bitrate:     445.7,
	}

	if got.PHash != want.PHash ||
		got.ResolutionX != want.ResolutionX ||
		got.ResolutionY != want.ResolutionY ||
		got.Duration != want.Duration ||
		got.Bitrate != want.Bitrate {
		t.Fatalf("unexpected fixture output:\n got: %+v\nwant: %+v", *got, want)
	}
}

func requireMediaTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(fmt.Sprintf("%s not found in PATH", tool))
		}
	}
}
