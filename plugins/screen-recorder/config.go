package recorder

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is the recorder request the settings declared.
type Config struct {
	VideoSource           string
	Directory             string
	FilenamePattern       string
	FrameRate             int
	VideoCodec            string
	VideoQP               int
	Resolution            string
	AudioSource           string
	AudioCodec            string
	AudioBitrate          int
	ShowCursor            bool
	ColorRange            string
	HideInactive          bool
	ReplayEnabled         bool
	ReplayDuration        int
	ReplayFilenamePattern string
	ReplayStorage         string
}

func ParseConfig(values map[string]any) (Config, error) {
	cfg := Config{
		VideoSource:           "portal",
		Directory:             "~/Videos/Recordings",
		FilenamePattern:       "recording_%Y%m%d_%H%M%S",
		FrameRate:             60,
		VideoCodec:            "h264",
		VideoQP:               25,
		Resolution:            "original",
		AudioSource:           "default_output",
		AudioCodec:            "opus",
		ShowCursor:            true,
		ColorRange:            "limited",
		ReplayDuration:        30,
		ReplayFilenamePattern: "replay_%Y%m%d_%H%M%S",
		ReplayStorage:         "ram",
	}
	if values == nil {
		values = map[string]any{}
	}
	if v, ok := values["video_source"].(string); ok && v != "" {
		cfg.VideoSource = v
	}
	if v, ok := values["directory"].(string); ok && v != "" {
		cfg.Directory = v
	}
	if v, ok := values["filename_pattern"].(string); ok && v != "" {
		cfg.FilenamePattern = v
	}
	if n, ok := asInt(values["frame_rate"]); ok {
		cfg.FrameRate = n
	}
	if v, ok := values["video_codec"].(string); ok && v != "" {
		cfg.VideoCodec = v
	}
	if n, ok := asInt(values["video_qp"]); ok {
		cfg.VideoQP = n
	}
	if v, ok := values["resolution"].(string); ok && v != "" {
		cfg.Resolution = v
	}
	if v, ok := values["audio_source"].(string); ok && v != "" {
		cfg.AudioSource = v
	}
	if v, ok := values["audio_codec"].(string); ok && v != "" {
		cfg.AudioCodec = v
	}
	if n, ok := asInt(values["audio_bitrate"]); ok {
		cfg.AudioBitrate = n
	}
	if v, ok := values["show_cursor"].(bool); ok {
		cfg.ShowCursor = v
	}
	if v, ok := values["color_range"].(string); ok && v != "" {
		cfg.ColorRange = v
	}
	if v, ok := values["hide_inactive"].(bool); ok {
		cfg.HideInactive = v
	}
	if v, ok := values["replay_enabled"].(bool); ok {
		cfg.ReplayEnabled = v
	}
	if n, ok := asInt(values["replay_duration"]); ok {
		cfg.ReplayDuration = n
	}
	if v, ok := values["replay_filename_pattern"].(string); ok && v != "" {
		cfg.ReplayFilenamePattern = v
	}
	if v, ok := values["replay_storage"].(string); ok && v != "" {
		cfg.ReplayStorage = v
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	if c.VideoSource != "portal" && c.VideoSource != "focused" {
		return fmt.Errorf("recorder: video source %q is not focused or portal", c.VideoSource)
	}
	if c.FrameRate < 1 || c.FrameRate > 240 {
		return fmt.Errorf("recorder: frame rate %d is outside 1 through 240", c.FrameRate)
	}
	if !knownVideoCodec[c.VideoCodec] {
		return fmt.Errorf("recorder: video codec %q is not supported", c.VideoCodec)
	}
	if c.VideoQP < 0 || c.VideoQP > 51 {
		return fmt.Errorf("recorder: video qp %d is outside 0 through 51", c.VideoQP)
	}
	if c.Resolution != "original" && !isWxH(c.Resolution) {
		return fmt.Errorf("recorder: resolution %q is not original or WxH", c.Resolution)
	}
	if !knownAudioSource[c.AudioSource] {
		return fmt.Errorf("recorder: audio source %q is not supported", c.AudioSource)
	}
	if !knownAudioCodec[c.AudioCodec] {
		return fmt.Errorf("recorder: audio codec %q is not supported", c.AudioCodec)
	}
	if c.AudioBitrate < 0 || c.AudioBitrate > 512 {
		return fmt.Errorf("recorder: audio bitrate %d is outside 0 through 512", c.AudioBitrate)
	}
	if c.ColorRange != "limited" && c.ColorRange != "full" {
		return fmt.Errorf("recorder: color range %q is not limited or full", c.ColorRange)
	}
	if c.ReplayStorage != "ram" && c.ReplayStorage != "disk" {
		return fmt.Errorf("recorder: replay storage %q is not ram or disk", c.ReplayStorage)
	}
	if c.ReplayDuration < 5 || c.ReplayDuration > 3600 {
		return fmt.Errorf("recorder: replay duration %d is outside 5 through 3600", c.ReplayDuration)
	}
	return nil
}

var (
	knownVideoCodec  = map[string]bool{"h264": true, "hevc": true, "av1": true, "vp8": true, "vp9": true}
	knownAudioSource = map[string]bool{"default_output": true, "default_input": true, "both": true, "none": true}
	knownAudioCodec  = map[string]bool{"opus": true, "aac": true, "flac": true}
)

// RecordArgs is the argv after the executable for a file recording.
func (c Config) RecordArgs(output, destFile string) ([]string, error) {
	if destFile == "" {
		return nil, fmt.Errorf("recorder: recording has no output file")
	}
	w, err := c.captureTarget(output)
	if err != nil {
		return nil, err
	}
	args := c.commonArgs(w)
	args = append(args, "-o", destFile)
	return args, nil
}

// ReplayArgs is the argv after the executable for a replay buffer.
func (c Config) ReplayArgs(output, destDir string) ([]string, error) {
	if !c.ReplayEnabled {
		return nil, fmt.Errorf("recorder: replay is disabled")
	}
	if destDir == "" {
		return nil, fmt.Errorf("recorder: replay has no output directory")
	}
	w, err := c.captureTarget(output)
	if err != nil {
		return nil, err
	}
	args := c.commonArgs(w)
	args = append(args, "-c", "mp4", "-r", strconv.Itoa(c.ReplayDuration), "-replay-storage", c.ReplayStorage, "-o", destDir, "-ro", destDir)
	return args, nil
}

func (c Config) captureTarget(output string) (string, error) {
	if c.VideoSource == "portal" {
		return "portal", nil
	}
	if output == "" {
		return "", fmt.Errorf("recorder: focused capture needs an output")
	}
	return output, nil
}

func (c Config) commonArgs(target string) []string {
	cursor := "no"
	if c.ShowCursor {
		cursor = "yes"
	}
	args := []string{
		"-w", target,
		"-f", strconv.Itoa(c.FrameRate),
		"-k", c.VideoCodec,
		"-bm", "qp",
		"-ffmpeg-opts", "qp=" + strconv.Itoa(scaledQP(c.VideoCodec, c.VideoQP)),
		"-cursor", cursor,
		"-cr", c.ColorRange,
		"-v", "no",
	}
	if c.Resolution != "original" {
		args = append(args, "-s", c.Resolution)
	}
	if c.AudioSource != "none" {
		args = append(args, "-ac", c.AudioCodec)
		if c.AudioBitrate > 0 {
			args = append(args, "-ab", strconv.Itoa(c.AudioBitrate))
		}
		a := c.AudioSource
		if a == "both" {
			a = "default_output|default_input"
		}
		args = append(args, "-a", a)
	}
	return args
}

func scaledQP(codec string, qp int) int {
	switch codec {
	case "vp8":
		return qp * 2
	case "av1", "vp9":
		return qp * 4
	}
	return qp
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func isWxH(s string) bool {
	w, h, ok := strings.Cut(s, "x")
	if !ok || w == "" || h == "" {
		return false
	}
	wi, errW := strconv.Atoi(w)
	hi, errH := strconv.Atoi(h)
	return errW == nil && errH == nil && wi > 0 && hi > 0
}
