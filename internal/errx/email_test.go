package errx

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestKnownIMAPConnectionMessageRequiresExactEnvelope(t *testing.T) {
	for _, stage := range []string{MailConnectionStageIMAPDial, MailConnectionStageIMAPTLS, MailConnectionStageIMAPSTARTTLS} {
		for _, reason := range []string{MailConnectionReasonDNSFailure, MailConnectionReasonDNSTimeout, MailConnectionReasonCertificate,
			MailConnectionReasonWorkerAddress, MailConnectionReasonRefused, MailConnectionReasonRoute, MailConnectionReasonClosed,
			MailConnectionReasonTimeout, MailConnectionReasonFailed, MailConnectionReasonPrivateHost} {
			e := ErrMailServerUnreachableAt(stage, errors.New(reason))
			if !IsKnownIMAPConnectionMessage(e.Message) || e.Code != MailErrorCodeServerUnreachable || e.ResolveMethod != MailErrorResolveMethodRetry {
				t.Fatal("known category rejected or retry semantics changed")
			}
			for _, malformed := range []string{e.Message + " private", "private " + e.Message, e.Message[:len(e.Message)-1]} {
				if IsKnownIMAPConnectionMessage(malformed) {
					t.Fatal("nonexact envelope recognized")
				}
			}
		}
	}
	for _, message := range []string{ErrMailServerUnreachable.Message, ErrMailServerUnreachableAt("SMTP dial", errors.New("connection refused")).Message,
		ErrMailServerUnreachableAt("IMAP unsupported", errors.New("connection refused")).Message,
		ErrMailServerUnreachableAt("IMAP dial", errors.New("private raw cause")).Message, ""} {
		if IsKnownIMAPConnectionMessage(message) {
			t.Fatal("unrecognized stage or cause accepted")
		}
	}
	if ErrMailServerUnreachableAt("", errors.New("raw")) != ErrMailServerUnreachable || ErrMailServerUnreachableAt("IMAP dial", nil) != ErrMailServerUnreachable {
		t.Fatal("generic factory compatibility changed")
	}
}

func TestUnreachableMessagesSayWhatToDo(t *testing.T) {
	for _, tc := range []struct {
		reason, hint, title string
	}{
		{MailConnectionReasonPrivateHost, MailPrivateHostHint, "Server address is not public"},
		{MailConnectionReasonCertificate, MailCertificateHint, "Server certificate not verified"},
		{MailConnectionReasonNoSTARTTLS, MailNoSTARTTLSHint, "No encrypted connection offered"},
	} {
		e := ErrMailServerUnreachableAt(MailConnectionStageIMAPDial, errors.New(tc.reason))
		if !strings.HasSuffix(e.Message, tc.hint) {
			t.Fatalf("%s: message %q lacks its hint", tc.reason, e.Message)
		}
		// SMTP failures carry a status suffix after the hint.
		e.Message += " (SMTP dial status 0)"
		if info := e.GetUserErrorInfo(); info.Title != tc.title || !strings.Contains(info.ActionRequired, "Update credentials") {
			t.Fatalf("%s: got %+v", tc.reason, info)
		}
	}
	if info := ErrMailCleartextAuth.GetUserErrorInfo(); info.Title != "Encrypted sign-in not offered" {
		t.Fatalf("cleartext auth: got %+v", info)
	}
}

func TestKnownIMAPConnectionMessageAcceptsEarlierWording(t *testing.T) {
	legacy := fmt.Sprintf(mailServerUnreachablePrefix, MailConnectionStageIMAPTLS, MailConnectionReasonCertificate) + mailServerUnreachableHint
	if !IsKnownIMAPConnectionMessage(legacy) {
		t.Fatal("message from an earlier worker rejected")
	}
	if !IsKnownIMAPConnectionMessage(ErrMailServerUnreachableAt(MailConnectionStageIMAPDial, errors.New(MailConnectionReasonPrivateHost)).Message) {
		t.Fatal("private host message rejected")
	}
}
