package githubnotifications

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxActivityWeeks = 54

const activityGraphQLQuery = `query($from: DateTime!, $to: DateTime!) {
  viewer {
    contributionsCollection(from: $from, to: $to) {
      contributionCalendar {
        totalContributions
        weeks {
          contributionDays { date weekday contributionCount contributionLevel }
        }
      }
    }
  }
}`

type RawActivity struct {
	TotalContributions int               `json:"totalContributions"`
	Weeks              []RawActivityWeek `json:"weeks"`
}

type RawActivityWeek struct {
	Days []RawContributionDay `json:"contributionDays"`
}

type RawContributionDay struct {
	Date              string `json:"date"`
	Weekday           int    `json:"weekday"`
	ContributionCount int    `json:"contributionCount"`
	ContributionLevel string `json:"contributionLevel"`
	Color             string `json:"color"`
}

type ContributionLevel string

const (
	ContributionNone           ContributionLevel = "NONE"
	ContributionFirstQuartile  ContributionLevel = "FIRST_QUARTILE"
	ContributionSecondQuartile ContributionLevel = "SECOND_QUARTILE"
	ContributionThirdQuartile  ContributionLevel = "THIRD_QUARTILE"
	ContributionFourthQuartile ContributionLevel = "FOURTH_QUARTILE"
)

type ContributionDay struct {
	Date  string            `json:"date,omitempty"`
	Count int               `json:"count"`
	Level ContributionLevel `json:"level"`
}

type ActivityWeek struct {
	Days [7]ContributionDay `json:"days"`
}

type Activity struct {
	TotalContributions int            `json:"total_contributions"`
	ActiveDays         int            `json:"active_days"`
	Weeks              []ActivityWeek `json:"weeks"`
}

func BuildActivityArgs(from, to time.Time) ([]string, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > 366*24*time.Hour {
		return nil, fmt.Errorf("activity range must be positive and no longer than 366 days")
	}
	return []string{
		"api", "graphql", "-f", "query=" + activityGraphQLQuery,
		"-F", "from=" + from.UTC().Format(time.RFC3339),
		"-F", "to=" + to.UTC().Format(time.RFC3339),
	}, nil
}

func ParseActivityResponse(data []byte) (RawActivity, error) {
	var response struct {
		Data *struct {
			Viewer *struct {
				ContributionsCollection *struct {
					ContributionCalendar *RawActivity `json:"contributionCalendar"`
				} `json:"contributionsCollection"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return RawActivity{}, fmt.Errorf("decode activity response: %w", err)
	}
	if len(response.Errors) != 0 {
		messages := make([]string, 0, len(response.Errors))
		for _, graphErr := range response.Errors {
			if strings.TrimSpace(graphErr.Message) != "" {
				messages = append(messages, graphErr.Message)
			}
		}
		if len(messages) == 0 {
			return RawActivity{}, fmt.Errorf("GitHub GraphQL activity query failed")
		}
		return RawActivity{}, fmt.Errorf("GitHub GraphQL activity query failed: %s", strings.Join(messages, "; "))
	}
	if response.Data == nil || response.Data.Viewer == nil || response.Data.Viewer.ContributionsCollection == nil || response.Data.Viewer.ContributionsCollection.ContributionCalendar == nil {
		return RawActivity{}, fmt.Errorf("GitHub GraphQL response omitted the contribution calendar")
	}
	return *response.Data.Viewer.ContributionsCollection.ContributionCalendar, nil
}

func NormalizeActivity(raw RawActivity) (Activity, error) {
	if raw.TotalContributions < 0 || len(raw.Weeks) == 0 || len(raw.Weeks) > maxActivityWeeks {
		return Activity{}, fmt.Errorf("invalid contribution calendar bounds")
	}
	activity := Activity{
		TotalContributions: raw.TotalContributions,
		Weeks:              make([]ActivityWeek, len(raw.Weeks)),
	}
	seen := make(map[string]bool)
	previous := time.Time{}
	total := 0
	maxInt := int(^uint(0) >> 1)
	for wi, week := range raw.Weeks {
		if len(week.Days) == 0 || len(week.Days) > 7 {
			return Activity{}, fmt.Errorf("week %d has invalid day count %d", wi, len(week.Days))
		}
		lastWeekday := -1
		for _, rawDay := range week.Days {
			dayDate, err := time.Parse("2006-01-02", rawDay.Date)
			if err != nil || dayDate.Format("2006-01-02") != rawDay.Date {
				return Activity{}, fmt.Errorf("invalid contribution date %q", rawDay.Date)
			}
			weekday := int(dayDate.Weekday())
			if rawDay.Weekday != weekday || rawDay.Weekday <= lastWeekday {
				return Activity{}, fmt.Errorf("invalid weekday %d for contribution date %s", rawDay.Weekday, rawDay.Date)
			}
			if seen[rawDay.Date] || (!previous.IsZero() && !dayDate.Equal(previous.AddDate(0, 0, 1))) {
				return Activity{}, fmt.Errorf("contribution dates are duplicated or not consecutive at %s", rawDay.Date)
			}
			level := ContributionLevel(rawDay.ContributionLevel)
			if !validContributionLevel(level) || rawDay.ContributionCount < 0 {
				return Activity{}, fmt.Errorf("invalid contribution value for %s", rawDay.Date)
			}
			if (rawDay.ContributionCount == 0) != (level == ContributionNone) {
				return Activity{}, fmt.Errorf("contribution level does not match count for %s", rawDay.Date)
			}
			if rawDay.ContributionCount > maxInt-total {
				return Activity{}, fmt.Errorf("contribution total overflows")
			}
			total += rawDay.ContributionCount
			if rawDay.ContributionCount > 0 {
				activity.ActiveDays++
			}
			activity.Weeks[wi].Days[weekday] = ContributionDay{
				Date: rawDay.Date, Count: rawDay.ContributionCount, Level: level,
			}
			seen[rawDay.Date] = true
			previous = dayDate
			lastWeekday = weekday
		}
	}
	if total != raw.TotalContributions {
		return Activity{}, fmt.Errorf("contribution total %d does not match daily sum %d", raw.TotalContributions, total)
	}
	return activity, nil
}

func validContributionLevel(level ContributionLevel) bool {
	switch level {
	case ContributionNone, ContributionFirstQuartile, ContributionSecondQuartile, ContributionThirdQuartile, ContributionFourthQuartile:
		return true
	default:
		return false
	}
}
