package recorder

import (
	"strings"
	"testing"
)

func TestParseConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VideoSource != "portal" || cfg.FrameRate != 60 || cfg.VideoCodec != "h264" || cfg.VideoQP != 25 {
		t.Fatalf("%+v", cfg)
	}
	if cfg.Directory != "~/Videos/Recordings" || cfg.FilenamePattern != "recording_%Y%m%d_%H%M%S" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.AudioSource != "default_output" || cfg.AudioCodec != "opus" || cfg.AudioBitrate != 0 {
		t.Fatalf("%+v", cfg)
	}
	if !cfg.ShowCursor || cfg.ColorRange != "limited" || cfg.HideInactive || cfg.ReplayEnabled {
		t.Fatalf("%+v", cfg)
	}
	if cfg.ReplayDuration != 30 || cfg.ReplayStorage != "ram" {
		t.Fatalf("%+v", cfg)
	}
}

func TestParseConfigRejectsInvalidCombinations(t *testing.T) {
	t.Parallel()
	cases := []map[string]any{
		{"video_source": "region"},
		{"frame_rate": 0.0},
		{"frame_rate": 241.0},
		{"video_codec": "mpeg2"},
		{"video_qp": -1.0},
		{"video_qp": 52.0},
		{"resolution": "1080p"},
		{"audio_source": "hdmi"},
		{"audio_codec": "mp3"},
		{"audio_bitrate": 513.0},
		{"color_range": "auto"},
		{"replay_storage": "nvme"},
		{"replay_duration": 4.0},
		{"replay_duration": 3601.0},
	}
	for _, values := range cases {
		if _, err := ParseConfig(values); err == nil {
			t.Errorf("ParseConfig(%v) accepted", values)
		}
	}
}

func TestConfigRecordArgsUsesPortalAndFocusedConnector(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(map[string]any{"video_source": "portal"})
	if err != nil {
		t.Fatal(err)
	}
	args, err := cfg.RecordArgs("", "/tmp/out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(args, "-w", "portal") || !hasPair(args, "-o", "/tmp/out.mp4") {
		t.Fatalf("portal args = %v", args)
	}
	if shellText(args) {
		t.Fatalf("args look like a shell line: %v", args)
	}

	cfg.VideoSource = "focused"
	if _, err := cfg.RecordArgs("", "/tmp/out.mp4"); err == nil {
		t.Fatal("focused capture without a connector was accepted")
	}
	args, err = cfg.RecordArgs("DP-1", "/tmp/out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(args, "-w", "DP-1") {
		t.Fatalf("focused args = %v", args)
	}
}

func TestConfigRecordArgsCarriesCodecQualityCursorAudioAndResolution(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(map[string]any{
		"frame_rate":    30.0,
		"video_codec":   "av1",
		"video_qp":      20.0,
		"resolution":    "1920x1080",
		"audio_source":  "both",
		"audio_codec":   "flac",
		"audio_bitrate": 192.0,
		"show_cursor":   false,
		"color_range":   "full",
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := cfg.RecordArgs("DP-1", "/tmp/out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"-f", "30"}, {"-k", "av1"}, {"-bm", "qp"}, {"-ffmpeg-opts", "qp=80"},
		{"-s", "1920x1080"}, {"-ac", "flac"}, {"-ab", "192"},
		{"-a", "default_output|default_input"}, {"-cursor", "no"}, {"-cr", "full"}, {"-v", "no"},
	} {
		if !hasPair(args, pair[0], pair[1]) {
			t.Errorf("missing %s %s in %v", pair[0], pair[1], args)
		}
	}
}

func TestConfigRecordArgsOmitsAudioWhenNone(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(map[string]any{"audio_source": "none"})
	if err != nil {
		t.Fatal(err)
	}
	args, err := cfg.RecordArgs("", "/tmp/out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if hasFlag(args, "-a") || hasFlag(args, "-ac") || hasFlag(args, "-ab") {
		t.Fatalf("none still requested audio: %v", args)
	}
}

func TestConfigReplayArgsRequireEnableAndUseReplayDirectory(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ReplayArgs("DP-1", "/tmp/replays"); err == nil {
		t.Fatal("replay args accepted while replay is disabled")
	}
	cfg, err = ParseConfig(map[string]any{
		"replay_enabled":  true,
		"replay_duration": 12.0,
		"replay_storage":  "disk",
		"video_source":    "portal",
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := cfg.ReplayArgs("", "/tmp/replays")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(args, "-r", "12") || !hasPair(args, "-replay-storage", "disk") || !hasPair(args, "-ro", "/tmp/replays") || !hasPair(args, "-o", "/tmp/replays") {
		t.Fatalf("replay args = %v", args)
	}
	if shellText(args) {
		t.Fatalf("replay args look like a shell line: %v", args)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func shellText(args []string) bool {
	joined := strings.Join(args, " ")
	return strings.Contains(joined, ">>") || strings.Contains(joined, "&&") || strings.Contains(args[0], " ")
}
