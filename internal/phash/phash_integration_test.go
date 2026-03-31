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

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "resources", "tests__LosAlamosPhysicalSimulations_1m.mp4"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	if _, err := os.Stat(fixturePath); err != nil {
		t.Skipf("fixture not found: %s (%v)", fixturePath, err)
	}
	t.Logf("testing %s", fixturePath)

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

	assertEqualField(t, "phash", got.PHash, want.PHash)
	assertEqualField(t, "duration", got.Duration, want.Duration)
	assertEqualField(t, "resolution_x", got.ResolutionX, want.ResolutionX)
	assertEqualField(t, "resolution_y", got.ResolutionY, want.ResolutionY)
	assertEqualField(t, "bitrate", got.Bitrate, want.Bitrate)
}

func requireMediaTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(fmt.Sprintf("%s not found in PATH", tool))
		}
	}
}

func assertEqualField[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s mismatch: got=%v want=%v", name, got, want)
	}
	t.Logf("%s: %v [OK]", name, got)
}
