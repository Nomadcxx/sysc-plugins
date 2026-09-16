package githubnotifications

import (
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestTreesValidate(t *testing.T) {
	items := NormalizeList([]RawItem{rawItem("1", "Fix the thing", "PullRequest", "review_requested"), rawItem("2", "CI failed", "CheckSuite", "ci_activity")})

	bar := BarTree("3", true)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree("", false), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree("3 unread · updated 10:00"), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree("3 unread notifications", "", items), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree("No unread notifications", "gh exploded", nil), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
}
