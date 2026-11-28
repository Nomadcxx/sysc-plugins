package google

import "context"

// Mail sends plain-text mail through gog; it satisfies remind.Mailer.
type Mail struct {
	R  Runner
	To string
}

func (m *Mail) Send(ctx context.Context, subject, body string) error {
	_, err := m.R.RunStdin(ctx, body, "gmail", "send", "--to", m.To, "--subject", subject, "--body-file", "-")
	return err
}
