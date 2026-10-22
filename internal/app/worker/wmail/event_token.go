package wmail

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/app/mailcred"
	"github.com/warmbly/warmbly/internal/models"
	"golang.org/x/oauth2"
)

var errNoOrganizationKey = errors.New("no organization key for this mailbox")

func (w *WMail) onTokenUpdate(token *oauth2.Token) error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if w.stopped {
		return nil
	}
	sealed := false
	if data := w.executionData; data != nil && !data.Brokered {
		previous := ExecutionKey(data)
		if data.Google != nil {
			data.Google.Token = token
		}
		if data.Graph != nil {
			data.Graph.Token = token
		}
		w.executionKeys = map[[32]byte]struct{}{w.initialExecutionKey: {}, previous: {}, ExecutionKey(data): {}}
		sealed = data.SealedSecrets != ""
	}
	update := &models.JobEventTokenUpdate{
		UserID:       w.UserID,
		EmailID:      w.ID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
	// A mailbox that arrived sealed answers sealed; its control plane opens either form.
	if sealed {
		if err := w.sealTokenUpdate(update, token); err != nil {
			log.Warn().Err(err).Str("email_id", w.ID.String()).Msg("could not seal a refreshed token; sending it unsealed so the grant is not lost")
		}
	}
	return w.onEvent(models.JobEventTypeTokenUpdate, update)
}

// tokenSealTimeout bounds the key lookup behind sealing a refreshed token.
const tokenSealTimeout = 30 * time.Second

func (w *WMail) sealTokenUpdate(update *models.JobEventTokenUpdate, token *oauth2.Token) error {
	if w.CipherService == nil || w.OrgID == nil {
		return errNoOrganizationKey
	}
	ctx := w.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tokenSealTimeout)
	defer cancel()
	c, err := w.CipherService.Cipher(ctx, *w.OrgID)
	if err != nil {
		return err
	}
	v, err := mailcred.SealWorkerSecrets(ctx, c, &mailcred.WorkerSecrets{Token: token})
	if err != nil {
		return err
	}
	update.SealedTokens, update.AccessToken, update.RefreshToken = v, "", ""
	return nil
}
