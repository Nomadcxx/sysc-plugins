package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/aiusage"
)

func main() {
	reg := aiusage.Registry{
		"claude":      aiusage.NewOAuthUsage,
		"codex":       aiusage.NewSnapshot,
		"commandcode": aiusage.NewCommandCode,
		"copilot":     aiusage.NewCopilot,
		"minimax":     aiusage.NewMinimax,
		"ollama":      aiusage.NewOllama,
		"opencode-go": aiusage.NewOpenCodeGo,
		"synthetic":   aiusage.NewSynthetic,
	}
	order := []string{"claude", "codex", "commandcode", "copilot", "minimax", "ollama", "opencode-go", "synthetic"}
	for _, id := range order {
		col, ok := reg.Build(id, aiusage.Env{})
		if !ok {
			fmt.Printf("%-12s NO COLLECTOR\n", id)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		rep, err := col.Fetch(ctx)
		cancel()
		fmt.Printf("%-12s state=%d stale=%v updated=%s err=%q fetchErr=%v\n", id, rep.State, rep.Stale, rep.UpdatedAt.Format(time.RFC3339), rep.Err, err)
		for _, w := range rep.Windows {
			fmt.Printf("    window %s label=%q pct=%v hasPct=%v mins=%d resets=%s display=%q\n", w.Key, w.Label, w.UsedPercent, w.HasPercent, w.WindowMinutes, w.ResetsAt.Format(time.RFC3339), w.DisplayValue)
		}
		if rep.Plan != "" {
			fmt.Printf("    plan=%q\n", rep.Plan)
		}
	}
}
