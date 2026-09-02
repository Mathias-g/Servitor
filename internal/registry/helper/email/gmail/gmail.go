// Package gmail is the Google Workspace (Gmail) provider mechanism for the
// `email_received` trigger (SPEC: Triggers, ADR-0048). It knows how to talk to
// Gmail's IMAP server: how to connect, authenticate, find new (unseen)
// messages, and parse each into the provider-agnostic Email shape
// (internal/components/email). The trigger stays generic; this mechanism owns
// the provider-specific transport and auth (ADR-0027).
//
// It runs as a subprocess (ADR-0008): the worker invokes the servitor binary's
// hidden `__email_poll` command, which calls into this package. The mailbox
// password never appears in this package's arguments or inputs; it comes from
// the subprocess environment, filtered to the declared secret (SPEC: Varlock).
package gmail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Mathias-g/Servitor/internal/components/email"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
)

// Config is how the helper connects to a mailbox. Password is never carried
// here in arguments; it is supplied from the subprocess environment at dial
// time (SPEC: Varlock).
type Config struct {
	// Host is the IMAP server, for example "imap.gmail.com".
	Host string
	// Username is the mailbox account, for example "me@company.com".
	Username string
	// Password is the app password (or equivalent) for the account.
	Password string
	// Mailbox is the folder to watch; empty means "INBOX".
	Mailbox string
}

// Email is one parsed inbound message, the event payload a workflow receives.
type Email = email.Email

// Fetcher is an authenticated IMAP session over one mailbox.
type Fetcher struct {
	c   *client.Client
	cfg Config
}

// Dial connects to the mailbox and authenticates. Host defaults to Gmail's
// IMAP server on port 993 over TLS when no port is given.
func Dial(ctx context.Context, cfg Config) (*Fetcher, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("gmail: empty host")
	}
	addr := cfg.Host
	if !strings.Contains(addr, ":") {
		addr += ":993"
	}
	c, err := client.DialTLS(addr, nil)
	if err != nil {
		return nil, fmt.Errorf("gmail: connect %s: %w", addr, err)
	}
	if err := c.Login(cfg.Username, cfg.Password); err != nil {
		_ = c.Logout()
		return nil, fmt.Errorf("gmail: login %s: %w", cfg.Username, err)
	}
	return &Fetcher{c: c, cfg: cfg}, nil
}

// newFetcher wraps an already-connected client. It is used by tests to point at
// an in-memory IMAP server.
func newFetcher(c *client.Client, cfg Config) *Fetcher {
	return &Fetcher{c: c, cfg: cfg}
}

// FetchNew returns the messages in the mailbox that are new (not yet marked
// \Seen), parsing each into an Email, and marks them \Seen so a later poll only
// returns messages that arrived afterward. It returns the emails in mailbox
// order.
func (f *Fetcher) FetchNew() ([]Email, error) {
	mailbox := f.cfg.Mailbox
	if mailbox == "" {
		mailbox = "INBOX"
	}
	if _, err := f.c.Select(mailbox, false); err != nil {
		return nil, fmt.Errorf("gmail: select %s: %w", mailbox, err)
	}

	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	seqs, err := f.c.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("gmail: search unseen: %w", err)
	}
	if len(seqs) == 0 {
		return nil, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(seqs...)
	ch := make(chan *imap.Message, 10)
	go func() {
		_ = f.c.Fetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchRFC822}, ch)
	}()

	var emails []Email
	for msg := range ch {
		emails = append(emails, parseMessage(msg))
	}

	// Mark the fetched messages as seen so they are not returned again.
	if err := f.markSeen(seqset); err != nil {
		return emails, err
	}
	return emails, nil
}

// markSeen adds the \Seen flag to the given messages.
func (f *Fetcher) markSeen(seqset *imap.SeqSet) error {
	ch := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() {
		done <- f.c.Store(seqset, imap.AddFlags, []interface{}{imap.SeenFlag}, ch)
	}()
	for range ch {
	}
	if err := <-done; err != nil {
		return fmt.Errorf("gmail: mark seen: %w", err)
	}
	return nil
}

// Close logs out of the session.
func (f *Fetcher) Close() error {
	if f.c == nil {
		return nil
	}
	return f.c.Logout()
}

// parseMessage converts an IMAP message into an Email. Envelope fields come
// from the server; the body is parsed from the raw RFC 822 payload, preferring
// the first text part.
func parseMessage(msg *imap.Message) Email {
	e := Email{
		Subject:   msg.Envelope.Subject,
		Date:      msg.Envelope.Date,
		MessageID: msg.Envelope.MessageId,
	}
	if len(msg.Envelope.From) > 0 {
		e.From = addressString(msg.Envelope.From[0])
	}
	for _, a := range msg.Envelope.To {
		e.To = append(e.To, addressString(a))
	}
	e.Body = parseBody(msg)
	return e
}

// addressString formats an IMAP address as "Name <user@host>" when a name is
// present, otherwise just the mailbox address.
func addressString(a *imap.Address) string {
	addr := a.MailboxName + "@" + a.HostName
	if a.PersonalName != "" {
		return fmt.Sprintf("%s <%s>", a.PersonalName, addr)
	}
	return addr
}

// parseBody extracts the textual body from a message's raw payload (fetched
// with FetchRFC822), preferring the first text part.
func parseBody(msg *imap.Message) string {
	if len(msg.Body) == 0 {
		return ""
	}
	// The RFC822 payload is stored under whatever body-section key the server
	// returned; there is exactly one for a FetchRFC822 request.
	var lit imap.Literal
	for _, l := range msg.Body {
		lit = l
		break
	}
	if lit == nil {
		return ""
	}
	mr, err := mail.CreateReader(lit)
	if err != nil {
		return ""
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			if !strings.HasPrefix(ct, "text/") {
				continue
			}
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, p.Body); err != nil {
				return ""
			}
			return strings.TrimSpace(buf.String())
		}
	}
	return ""
}
