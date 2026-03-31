package phash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/corona10/goimagehash"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/bmp"
)

const (
	screenshotWidth = 160
	columns         = 5
	rows            = 5
	chunkCount      = columns * rows
)

type Result struct {
	PHash       string  `json:"phash"`
	ResolutionX int     `json:"resolution_x"`
	ResolutionY int     `json:"resolution_y"`
	Dimensions  string  `json:"dimensions"`
	Duration    float64 `json:"duration"`
	Bitrate     float64 `json:"bitrate"`
	FrameRate   float64 `json:"frame_rate"`
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
		AvgRate   string `json:"avg_frame_rate"`
		RRate     string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func Compute(ctx context.Context, path string) (*Result, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat input file: %w", err)
	}

	meta, err := probe(ctx, path)
	if err != nil {
		return nil, err
	}

	duration := round(meta.duration, 2)
	bitrate := 0.0
	if meta.duration > 0 {
		bitrate = round((float64(stat.Size())*8.0)/(meta.duration*1000.0), 1)
	}

	ph, err := generatePHash(ctx, path, meta.duration)
	if err != nil {
		return nil, err
	}

	return &Result{
		PHash:       fmt.Sprintf("%016x", ph),
		ResolutionX: meta.width,
		ResolutionY: meta.height,
		Dimensions:  fmt.Sprintf("%d x %d", meta.width, meta.height),
		Duration:    duration,
		Bitrate:     bitrate,
		FrameRate:   round(meta.frameRate, 2),
	}, nil
}

type probedMeta struct {
	width    int
	height   int
	duration float64
	frameRate float64
}

func probe(ctx context.Context, path string) (*probedMeta, error) {
	cmd := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w: %s", err, out.String())
	}

	var p ffprobeOutput
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	var width, height int
	frameRate := 0.0
	for _, s := range p.Streams {
		if s.CodecType == "video" {
			width = s.Width
			height = s.Height
			frameRate = parseFrameRate(s.AvgRate)
			if frameRate <= 0 {
				frameRate = parseFrameRate(s.RRate)
			}
			break
		}
	}
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("no video stream dimensions found")
	}

	duration, err := parseDuration(p.Format.Duration)
	if err != nil || duration <= 0 {
		for _, s := range p.Streams {
			if s.CodecType == "video" {
				if d, derr := parseDuration(s.Duration); derr == nil && d > 0 {
					duration = d
					break
				}
			}
		}
	}
	if duration <= 0 {
		return nil, fmt.Errorf("could not determine video duration")
	}

	return &probedMeta{
		width:    width,
		height:   height,
		duration: duration,
		frameRate: frameRate,
	}, nil
}

func parseDuration(s string) (float64, error) {
	var d float64
	if s == "" || s == "N/A" {
		return 0, fmt.Errorf("duration missing")
	}
	_, err := fmt.Sscanf(s, "%f", &d)
	if err != nil {
		return 0, err
	}
	return d, nil
}

func parseFrameRate(s string) float64 {
	v := strings.TrimSpace(s)
	if v == "" || v == "N/A" || v == "0/0" {
		return 0
	}
	if strings.Contains(v, "/") {
		parts := strings.SplitN(v, "/", 2)
		if len(parts) != 2 {
			return 0
		}
		num, errNum := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		den, errDen := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if errNum != nil || errDen != nil || den == 0 {
			return 0
		}
		return num / den
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}

func generatePHash(ctx context.Context, path string, duration float64) (uint64, error) {
	images := make([]image.Image, 0, chunkCount)
	offset := 0.05 * duration
	step := (0.9 * duration) / float64(chunkCount)

	for i := 0; i < chunkCount; i++ {
		t := offset + (float64(i) * step)
		img, err := screenshot(ctx, path, t)
		if err != nil {
			return 0, fmt.Errorf("generate screenshot %d: %w", i, err)
		}
		images = append(images, img)
	}

	sprite := combineImages(images)
	hash, err := goimagehash.PerceptionHash(sprite)
	if err != nil {
		return 0, fmt.Errorf("compute phash: %w", err)
	}
	return hash.GetHash(), nil
}

func screenshot(ctx context.Context, path string, t float64) (image.Image, error) {
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		sctx,
		"ffmpeg",
		"-v", "error",
		"-y",
		"-ss", fmt.Sprintf("%f", t),
		"-i", path,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", screenshotWidth),
		"-c:v", "bmp",
		"-f", "rawvideo",
		"-",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.String())
	}

	img, _, err := image.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("decode bmp: %w", err)
	}
	return img, nil
}

func combineImages(images []image.Image) image.Image {
	width := images[0].Bounds().Dx()
	height := images[0].Bounds().Dy()
	canvasWidth := width * columns
	canvasHeight := height * rows
	montage := imaging.New(canvasWidth, canvasHeight, color.NRGBA{})
	for i, img := range images {
		x := width * (i % columns)
		y := height * int(math.Floor(float64(i)/float64(rows)))
		montage = imaging.Paste(montage, img, image.Pt(x, y))
	}
	return montage
}

func round(val float64, places int) float64 {
	pow := math.Pow(10, float64(places))
	return math.Round(val*pow) / pow
}
