package githubnotifications

import "testing"

func TestNormalizeActivityBuildsWeekdayCellsAndCounts(t *testing.T) {
	raw := RawActivity{
		TotalContributions: 3,
		Weeks: []RawActivityWeek{{Days: []RawContributionDay{
			{Date: "2026-09-13", Weekday: 0, ContributionCount: 0, ContributionLevel: "NONE"},
			{Date: "2026-09-14", Weekday: 1, ContributionCount: 2, ContributionLevel: "FIRST_QUARTILE", Color: "#ff00ff"},
			{Date: "2026-09-15", Weekday: 2, ContributionCount: 1, ContributionLevel: "FIRST_QUARTILE"},
		}}},
	}
	activity, err := NormalizeActivity(raw)
	if err != nil {
		t.Fatal(err)
	}
	if activity.TotalContributions != 3 || activity.ActiveDays != 2 || len(activity.Weeks) != 1 {
		t.Fatalf("activity = %+v", activity)
	}
	week := activity.Weeks[0]
	if week.Days[0].Date != "2026-09-13" || week.Days[1].Count != 2 || week.Days[2].Date != "2026-09-15" || week.Days[3].Date != "" {
		t.Fatalf("weekday cells = %+v", week.Days)
	}
}

func TestNormalizeActivityRejectsMalformedDaysAndTotals(t *testing.T) {
	valid := RawActivity{TotalContributions: 1, Weeks: []RawActivityWeek{{Days: []RawContributionDay{
		{Date: "2026-09-14", Weekday: 1, ContributionCount: 1, ContributionLevel: "FIRST_QUARTILE"},
	}}}}
	cases := []struct {
		name   string
		mutate func(*RawActivity)
	}{
		{"bad date", func(a *RawActivity) { a.Weeks[0].Days[0].Date = "2026-02-30" }},
		{"bad weekday", func(a *RawActivity) { a.Weeks[0].Days[0].Weekday = 2 }},
		{"negative count", func(a *RawActivity) { a.Weeks[0].Days[0].ContributionCount = -1 }},
		{"unknown level", func(a *RawActivity) { a.Weeks[0].Days[0].ContributionLevel = "NEON" }},
		{"inconsistent total", func(a *RawActivity) { a.TotalContributions = 2 }},
		{"duplicate day", func(a *RawActivity) { a.Weeks = append(a.Weeks, a.Weeks[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := valid
			raw.Weeks = append([]RawActivityWeek(nil), valid.Weeks...)
			raw.Weeks[0].Days = append([]RawContributionDay(nil), valid.Weeks[0].Days...)
			tc.mutate(&raw)
			if _, err := NormalizeActivity(raw); err == nil {
				t.Fatal("malformed activity accepted")
			}
		})
	}
}
