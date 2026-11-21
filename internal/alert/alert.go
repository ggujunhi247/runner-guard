// Package alert posts one-line messages to a chat webhook.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// client has its own timeout; http.DefaultClient has none.
var client = &http.Client{Timeout: 15 * time.Second}

// Send POSTs {"text": msg, "content": msg}: Slack-style webhooks read
// "text", Discord reads "content".
func Send(ctx context.Context, webhook, msg string) error {
	body, _ := json.Marshal(map[string]string{"text": msg, "content": msg})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return scrub(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook answered %s", resp.Status)
	}
	return nil
}

// scrub drops the URL from an error: the webhook path is the secret, and
// errors end up in logs.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("webhook %s: %w", ue.Op, ue.Err)
	}
	return err
}
