package githubnotifications

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GH runs the gh CLI. It is an interface so the service's optimistic-update
// behavior can be tested without GitHub.
type GH interface {
	Notifications(ctx context.Context, page, perPage int) ([]RawItem, error)
	Search(ctx context.Context, kind WorkKind, page, perPage int) (RawWorkPage, error)
	Activity(ctx context.Context, from, to time.Time) (RawActivity, error)
	MarkRead(ctx context.Context, id string) error
	MarkAll(ctx context.Context) error
}

// CLI implements GH by shelling out to gh, like the original plugin.
type CLI struct{}

// ErrorKind classifies a gh failure the way the original's classifyError does.
type ErrorKind string

const (
	ErrAuth        ErrorKind = "auth_error"
	ErrRateLimited ErrorKind = "rate_limited"
	ErrNetwork     ErrorKind = "error"
)

// Classify maps stderr text to a coarse failure kind for the tooltip.
func Classify(stderr string) ErrorKind {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate-limit") || strings.Contains(lower, "secondary rate"):
		return ErrRateLimited
	case strings.Contains(lower, "403") || strings.Contains(lower, "permission") || strings.Contains(lower, "scope") ||
		strings.Contains(lower, "sso") || strings.Contains(lower, "auth") || strings.Contains(lower, "token") ||
		strings.Contains(lower, "login") || strings.Contains(lower, "401"):
		return ErrAuth
	default:
		return ErrNetwork
	}
}

func runGH(ctx context.Context, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func BuildNotificationArgs(perPage int) []string {
	if perPage < 1 {
		perPage = 1
	}
	perPage = min(perPage, 100)
	return []string{"api", fmt.Sprintf("notifications?all=false&per_page=%d", perPage)}
}

func BuildNotificationPageArgs(page, perPage int) []string {
	if page < 1 {
		page = 1
	}
	if page == 1 {
		return BuildNotificationArgs(perPage)
	}
	if perPage < 1 {
		perPage = 1
	}
	perPage = min(perPage, 100)
	return []string{"api", fmt.Sprintf("notifications?all=false&per_page=%d&page=%d", perPage, page)}
}

func (CLI) Notifications(ctx context.Context, page, perPage int) ([]RawItem, error) {
	stdout, stderr, err := runGH(ctx, BuildNotificationPageArgs(page, perPage)...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr)
	}
	var raw []RawItem
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Refresh retains the page-one method for older callers.
func (cli CLI) Refresh(ctx context.Context, perPage int) ([]RawItem, error) {
	return cli.Notifications(ctx, 1, perPage)
}

func (CLI) Search(ctx context.Context, kind WorkKind, page, perPage int) (RawWorkPage, error) {
	args, err := BuildWorkArgs(kind, page, perPage)
	if err != nil {
		return RawWorkPage{}, err
	}
	stdout, stderr, err := runGH(ctx, args...)
	if err != nil {
		return RawWorkPage{}, fmt.Errorf("%w: %s", err, stderr)
	}
	var raw RawWorkPage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return RawWorkPage{}, err
	}
	return raw, nil
}

func (CLI) Activity(ctx context.Context, from, to time.Time) (RawActivity, error) {
	args, err := BuildActivityArgs(from, to)
	if err != nil {
		return RawActivity{}, err
	}
	stdout, stderr, err := runGH(ctx, args...)
	if err != nil {
		return RawActivity{}, fmt.Errorf("%w: %s", err, stderr)
	}
	return ParseActivityResponse([]byte(stdout))
}

func (CLI) MarkRead(ctx context.Context, id string) error {
	if !ValidThreadID(id) {
		return fmt.Errorf("invalid thread id %q", id)
	}
	_, stderr, err := runGH(ctx, "api", "--method", "PATCH", "notifications/threads/"+id)
	if err != nil {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return nil
}

func (CLI) MarkAll(ctx context.Context) error {
	_, stderr, err := runGH(ctx, "api", "--method", "PUT", "notifications")
	if err != nil {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return nil
}
