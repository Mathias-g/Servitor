package gmail

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"
)

// startTestServer boots an in-memory IMAP server and returns its address and a
// backend with a user that owns the mailbox.
func startTestServer(t *testing.T) (string, backend.Backend) {
	t.Helper()
	be := memory.New()

	s := server.New(be)
	s.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = s.Serve(ln) }()
	return ln.Addr().String(), be
}

// createMessage adds an unread message (no \Seen flag) to the user's INBOX.
func createMessage(t *testing.T, be backend.Backend, subject, body string) {
	t.Helper()
	// The memory backend ships one default user: username / password.
	u, err := be.Login(nil, "username", "password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	mbox, err := u.GetMailbox("INBOX")
	if err != nil {
		t.Fatalf("get mailbox: %v", err)
	}
	raw := "From: sender@example.com\r\n" +
		"To: username@example.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-Id: <" + subject + "@example.com>\r\n" +
		"Date: Mon, 02 Jan 2026 10:00:00 +0000\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		body + "\r\n"
	if err := mbox.CreateMessage(nil, time.Now(), strings.NewReader(raw)); err != nil {
		t.Fatalf("create message: %v", err)
	}
}

// dial connects to the test server (plain, not TLS), logs in, and returns the
// authenticated client.
func dial(t *testing.T, addr string) *client.Client {
	t.Helper()
	c, err := client.Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Login("username", "password"); err != nil {
		t.Fatalf("login: %v", err)
	}
	t.Cleanup(func() { _ = c.Logout() })
	return c
}

func TestFetchNewReturnsUnseenAndParses(t *testing.T) {
	addr, be := startTestServer(t)
	createMessage(t, be, "Hello", "First body")
	createMessage(t, be, "World", "Second body")

	f := newFetcher(dial(t, addr), Config{Username: "username", Password: "password"})

	emails, err := f.FetchNew()
	if err != nil {
		t.Fatalf("FetchNew: %v", err)
	}
	if len(emails) != 2 {
		t.Fatalf("fetched %d emails, want 2", len(emails))
	}
	subjects := map[string]bool{}
	for _, e := range emails {
		subjects[e.Subject] = true
		if e.From == "" || len(e.To) == 0 || e.Body == "" {
			t.Fatalf("email %+v missing a field", e)
		}
	}
	if !subjects["Hello"] || !subjects["World"] {
		t.Fatalf("subjects = %v, want Hello and World", subjects)
	}
}

func TestFetchNewMarksSeenSoSecondCallIsEmpty(t *testing.T) {
	addr, be := startTestServer(t)
	createMessage(t, be, "Only", "body")

	f := newFetcher(dial(t, addr), Config{Username: "username", Password: "password"})

	if emails, _ := f.FetchNew(); len(emails) != 1 {
		t.Fatalf("first fetch got %d, want 1", len(emails))
	}
	// The message was marked \Seen, so a second poll returns nothing.
	if emails, _ := f.FetchNew(); len(emails) != 0 {
		t.Fatalf("second fetch got %d, want 0 (should be marked seen)", len(emails))
	}
}
