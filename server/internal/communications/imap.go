package communications

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

var ErrIMAPNotConfigured = errors.New("imap is not configured")

type IMAPConfig struct {
	Host, Username, Password, Mailbox string
	Port                              int
	Timeout                           time.Duration
}

type EmailMessage struct {
	UID      uint32    `json:"uid"`
	From     []string  `json:"from"`
	To       []string  `json:"to"`
	Subject  string    `json:"subject"`
	Date     time.Time `json:"date"`
	BodyText string    `json:"body_text"`
}

func IMAPConfigFromEnv() IMAPConfig {
	port := 993
	if raw := strings.TrimSpace(os.Getenv("IMAP_PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < 65536 {
			port = parsed
		}
	}
	return IMAPConfigFromEnvValues(map[string]string{
		"IMAP_HOST": os.Getenv("IMAP_HOST"), "IMAP_USERNAME": os.Getenv("IMAP_USERNAME"), "IMAP_PASSWORD": os.Getenv("IMAP_PASSWORD"),
		"IMAP_MAILBOX": os.Getenv("IMAP_MAILBOX"), "IMAP_PORT": strconv.Itoa(port), "IMAP_TIMEOUT_SECONDS": os.Getenv("IMAP_TIMEOUT_SECONDS"),
	})
}

func IMAPConfigFromEnvValues(values map[string]string) IMAPConfig {
	port := 993
	if parsed, err := strconv.Atoi(strings.TrimSpace(values["IMAP_PORT"])); err == nil && parsed > 0 && parsed < 65536 {
		port = parsed
	}
	timeout := 30 * time.Second
	if raw := strings.TrimSpace(values["IMAP_TIMEOUT_SECONDS"]); raw != "" {
		if parsed, err := time.ParseDuration(raw + "s"); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	mailbox := strings.TrimSpace(values["IMAP_MAILBOX"])
	if mailbox == "" {
		mailbox = "INBOX"
	}
	return IMAPConfig{Host: strings.TrimSpace(values["IMAP_HOST"]), Username: strings.TrimSpace(values["IMAP_USERNAME"]), Password: values["IMAP_PASSWORD"], Mailbox: mailbox, Port: port, Timeout: timeout}
}

type EmailPage struct {
	Messages      []EmailMessage `json:"messages"`
	NextBeforeUID uint32         `json:"next_before_uid,omitempty"`
}
type IMAPReceiver struct {
	cfg       IMAPConfig
	tlsConfig *tls.Config
}

func NewIMAPReceiver(cfg IMAPConfig) (*IMAPReceiver, error) {
	if cfg.Host == "" || cfg.Username == "" || cfg.Password == "" || cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, ErrIMAPNotConfigured
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &IMAPReceiver{cfg: cfg, tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}}, nil
}

func (r *IMAPReceiver) ListUnread(ctx context.Context, beforeUID uint32) (*EmailPage, error) {
	if r == nil {
		return nil, ErrIMAPNotConfigured
	}
	callCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(callCtx, "tcp", net.JoinHostPort(r.cfg.Host, strconv.Itoa(r.cfg.Port)))
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(callCtx, func() { _ = conn.Close() })
	defer stop()
	deadline, ok := callCtx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	}
	tlsConn := tls.Client(conn, r.tlsConfig)
	if err := tlsConn.HandshakeContext(callCtx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	client := imapclient.New(tlsConn, nil)
	defer client.Close()
	if err := client.Login(r.cfg.Username, r.cfg.Password).Wait(); err != nil {
		return nil, errors.New("imap login failed")
	}
	selected, err := client.Select(r.cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, errors.New("imap select failed")
	}
	end := uint32(selected.UIDNext)
	if beforeUID > 0 && beforeUID < end {
		end = beforeUID
	}
	if end <= 1 {
		return &EmailPage{Messages: []EmailMessage{}}, nil
	}
	start := uint32(1)
	if end > 1000 {
		start = end - 1000
	}
	var uids imap.UIDSet
	uids.AddRange(imap.UID(start), imap.UID(end-1))
	search, err := client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{uids}, NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return nil, errors.New("imap search failed")
	}
	ids := search.AllUIDs()
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	page := &EmailPage{Messages: []EmailMessage{}}
	if start > 1 {
		page.NextBeforeUID = start
	}
	if len(ids) > 20 {
		ids = ids[:20]
		page.NextBeforeUID = uint32(ids[len(ids)-1])
	}
	if len(ids) == 0 {
		return page, nil
	}
	section := &imap.FetchItemBodySection{Peek: true, Partial: &imap.SectionPartial{Offset: 0, Size: 64 << 10}}
	messages, err := client.Fetch(imap.UIDSetNum(ids...), &imap.FetchOptions{UID: true, Envelope: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return nil, errors.New("imap fetch failed")
	}
	out := make([]EmailMessage, 0, len(messages))
	for _, msg := range messages {
		item := EmailMessage{UID: uint32(msg.UID), BodyText: readableMailBody(msg.FindBodySection(section))}
		if msg.Envelope != nil {
			item.Subject, item.Date = msg.Envelope.Subject, msg.Envelope.Date
			item.From, item.To = formatAddresses(msg.Envelope.From), formatAddresses(msg.Envelope.To)
		}
		out = append(out, item)
	}
	page.Messages = out
	return page, nil
}

func formatAddresses(addresses []imap.Address) []string {
	out := make([]string, 0, len(addresses))
	for i := range addresses {
		if address := addresses[i].Addr(); address != "" {
			out = append(out, address)
		}
	}
	return out
}
