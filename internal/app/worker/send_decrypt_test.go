package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/app/worker/wmail"
	"github.com/warmbly/warmbly/internal/client/goog"
	"github.com/warmbly/warmbly/internal/infrastructure/codec"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/emsg"
)

type failingCipherService struct{}

func (failingCipherService) Cipher(context.Context, uuid.UUID) (*cipher.Cipher, error) {
	return nil, errors.New("key store unavailable")
}

func TestSendThatCannotBeDecryptedFailsWithoutSending(t *testing.T) {
	keys := cipher.NewStatic(bytes.Repeat([]byte{5}, 32))
	orgID := uuid.New()
	c, err := keys.Cipher(t.Context(), orgID)
	if err != nil {
		t.Fatal(err)
	}
	seal := func(s string) string {
		out, err := c.Encrypt(t.Context(), s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	goodSubject, goodBody := seal("Hello Ann"), seal("Body for Ann")
	const garbage = "c2VjcmV0LWJ1dC1ub3QtY2lwaGVydGV4dA=="
	rawBody, err := base64.StdEncoding.DecodeString(goodBody)
	if err != nil {
		t.Fatal(err)
	}
	rawBody[len(rawBody)-1] ^= 1
	tamperedBody := base64.StdEncoding.EncodeToString(rawBody)

	cases := map[string]struct {
		keys          cipher.CipherService
		subject, body string
		html          string
		reason        string
	}{
		"subject undecryptable": {keys, garbage, goodBody, "", sendReasonUndecryptable},
		"plain undecryptable":   {keys, goodSubject, garbage, "", sendReasonUndecryptable},
		"html undecryptable":    {keys, goodSubject, goodBody, garbage, sendReasonUndecryptable},
		"short ciphertext":      {keys, goodSubject, "AAAA", "", sendReasonUndecryptable},
		"tampered body":         {keys, goodSubject, tamperedBody, "", sendReasonUndecryptable},
		"other workspace key":   {cipher.NewStatic(bytes.Repeat([]byte{6}, 32)), goodSubject, goodBody, "", sendReasonUndecryptable},
		"key unavailable":       {failingCipherService{}, goodSubject, goodBody, "", sendReasonKeyUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rt := &dispatchNativeTransport{}
			ctx := context.WithValue(t.Context(), oauth2.HTTPClient, &http.Client{Transport: rt})
			client := &goog.Client{Email: "sender@example.test"}
			if err := client.InitWithSource(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fixture-token"})); err != nil {
				t.Fatal(err)
			}
			bus := &filingBus{}
			w := newLoadingWorker(&capturedEvents{})
			w.ID, w.Bus, w.Codec, w.CipherService = uuid.NewString(), bus, codec.NewJSON(), tc.keys
			id := uuid.New()
			w.mailManager.Emails[id] = &wmail.WMail{OrgID: &orgID, EmailType: models.InboxProviderGoogle, GoogleData: &wmail.GoogleData{Client: client}}
			blob := &emsg.EmailBlob{PlainText: []byte(tc.body), HTMLBody: []byte(tc.html)}
			data, err := blob.EncodeBinary()
			if err != nil {
				t.Fatal(err)
			}
			w.Storage = dispatchBodyStore{data: data}
			command := models.SendEmail{TaskID: uuid.New(), EmailID: id, OrgID: orgID, To: []string{"recipient@example.test"}, Subject: tc.subject, BodyS3Key: "fixture"}
			if err := w.HandleSendEmail(ctx, command); err != nil {
				t.Fatalf("HandleSendEmail: %v", err)
			}
			if rt.calls.Load() != 0 {
				t.Fatal("a message that could not be opened reached the provider")
			}
			if len(bus.events) != 1 || bus.events[0].Type != models.JobEventTypeEmailFailed {
				t.Fatalf("events = %+v, want one EMAIL_FAILED", bus.events)
			}
			raw, err := json.Marshal(bus.events[0].Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), tc.reason) || strings.Contains(string(raw), garbage) || strings.Contains(string(raw), "key store") {
				t.Fatalf("failure = %s, want only %q", raw, tc.reason)
			}
		})
	}
}
