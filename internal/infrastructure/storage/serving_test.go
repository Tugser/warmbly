package storage

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestDownloadTypeNeverHandsBackADocumentType(t *testing.T) {
	for in, want := range map[string]string{
		"application/pdf":               "application/pdf",
		"image/png":                     "image/png",
		"text/csv; charset=utf-8":       "text/csv",
		"text/html":                     octetStream,
		"application/xhtml+xml":         octetStream,
		"image/svg+xml":                 octetStream,
		"text/xml":                      octetStream,
		"application/javascript":        octetStream,
		"text/css":                      octetStream,
		"":                              octetStream,
		"not a type":                    octetStream,
		"TEXT/HTML; charset=iso-8859-1": octetStream,
	} {
		if got := DownloadType(in); got != want {
			t.Fatalf("DownloadType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDownloadDispositionIsAlwaysAnAttachment(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":          `attachment; filename=report.pdf`,
		`a "quoted" name.txt`: `attachment; filename="a \"quoted\" name.txt"`,
		"../../etc/passwd":    `attachment; filename=passwd`,
		"":                    "attachment",
	} {
		if got := DownloadDisposition(in); got != want {
			t.Fatalf("DownloadDisposition(%q) = %q, want %q", in, got, want)
		}
	}
	if got := DownloadDisposition("résumé.pdf"); !strings.HasPrefix(got, "attachment; filename*=") {
		t.Fatalf("non-ASCII name: %q", got)
	}
}

func TestPublicHeadersInlineOnlyForRaster(t *testing.T) {
	if ct, d := PublicHeaders("image/jpeg"); ct != "image/jpeg" || d != "inline" {
		t.Fatalf("jpeg: %s %s", ct, d)
	}
	for _, in := range []string{"image/svg+xml", "text/html", ""} {
		if ct, d := PublicHeaders(in); ct != octetStream || d != "attachment" {
			t.Fatalf("%q: %s %s", in, ct, d)
		}
	}
}

func TestPresignedGetSignsDownloadOverrides(t *testing.T) {
	c := &Client{
		Client: s3.New(s3.Options{
			Region:       "us-east-1",
			Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
			BaseEndpoint: aws.String("https://store.example"),
			UsePathStyle: true,
		}),
		Bucket: "b",
	}
	raw, err := c.PresignedGetURL(context.Background(), "attachments/x/y", time.Minute,
		Download{Filename: "page.html", ContentType: "text/html"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response-content-disposition") != "attachment; filename=page.html" {
		t.Fatalf("disposition: %q", q.Get("response-content-disposition"))
	}
	if q.Get("response-content-type") != octetStream {
		t.Fatalf("type: %q", q.Get("response-content-type"))
	}

	raw, err = c.PresignedURL(context.Background(), PresignOpGet, "emails/x", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(raw)
	if u.Query().Get("response-content-disposition") != "attachment" {
		t.Fatalf("broker get disposition: %q", u.Query().Get("response-content-disposition"))
	}
}
