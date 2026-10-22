package mailcred

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/models"
	"golang.org/x/oauth2"
)

// WorkerSecrets is what a worker needs to sign in to a mailbox. It crosses the
// bus sealed under the mailbox's organization key, never in the clear.
type WorkerSecrets struct {
	Token *oauth2.Token   `json:"token,omitempty"`
	SMTP  *models.Service `json:"smtp,omitempty"`
	IMAP  *models.Service `json:"imap,omitempty"`
}

// SealWorkerSecrets seals secrets for a worker under an organization cipher.
func SealWorkerSecrets(ctx context.Context, c *cipher.Cipher, v *WorkerSecrets) (string, error) {
	if c == nil || v == nil {
		return "", errors.New("organization key unavailable")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return c.Encrypt(ctx, string(raw))
}

// OpenWorkerSecrets opens what SealWorkerSecrets produced.
func OpenWorkerSecrets(ctx context.Context, c *cipher.Cipher, sealed string) (*WorkerSecrets, error) {
	if c == nil {
		return nil, errors.New("organization key unavailable")
	}
	raw, err := c.Decrypt(ctx, sealed)
	if err != nil {
		return nil, err
	}
	var v WorkerSecrets
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// SealAddEmail moves the credentials of an ADD_EMAIL into SealedSecrets, leaving none in the clear.
func SealAddEmail(ctx context.Context, c *cipher.Cipher, e *models.AddWorkerEmail) error {
	var v WorkerSecrets
	switch {
	case e.Google != nil && e.Google.Token != nil:
		v.Token = e.Google.Token
	case e.Graph != nil && e.Graph.Token != nil:
		v.Token = e.Graph.Token
	case e.SmtpImap != nil && (e.SmtpImap.Credentials != nil || e.SmtpImap.Token != nil):
		if e.SmtpImap.Credentials != nil {
			v.SMTP, v.IMAP = e.SmtpImap.Credentials.SMTP, e.SmtpImap.Credentials.IMAP
		}
		v.Token = e.SmtpImap.Token
	default:
		return nil
	}
	sealed, err := SealWorkerSecrets(ctx, c, &v)
	if err != nil {
		return err
	}
	e.SealedSecrets = sealed
	if e.Google != nil {
		e.Google.Token = nil
	}
	if e.Graph != nil {
		e.Graph.Token = nil
	}
	if e.SmtpImap != nil {
		e.SmtpImap.Credentials, e.SmtpImap.Token = nil, nil
	}
	return nil
}

// OpenAddEmail restores the credentials SealAddEmail moved into SealedSecrets.
func OpenAddEmail(ctx context.Context, c *cipher.Cipher, e *models.AddWorkerEmail) error {
	if e.SealedSecrets == "" {
		return nil
	}
	v, err := OpenWorkerSecrets(ctx, c, e.SealedSecrets)
	if err != nil {
		return err
	}
	switch {
	case e.Google != nil:
		e.Google.Token = v.Token
	case e.Graph != nil:
		e.Graph.Token = v.Token
	case e.SmtpImap != nil:
		e.SmtpImap.Token = v.Token
		if v.SMTP != nil || v.IMAP != nil {
			e.SmtpImap.Credentials = &models.SmtpImap{SMTP: v.SMTP, IMAP: v.IMAP}
		}
	}
	return nil
}
