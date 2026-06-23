package imip

import (
	"bytes"
	"context"
	"log/slog"
	"net/mail"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// IMAPConfig describes the mailbox that receives iMIP replies.
type IMAPConfig struct {
	Host            string
	Port            int
	User            string
	Pass            string
	TLS             string // implicit, starttls, none
	Folder          string
	PollInterval    time.Duration
	ProcessedFolder string
}

// Receiver polls an IMAP mailbox for iTIP replies and applies them.
type Receiver struct {
	db     *storage.DB
	cfg    IMAPConfig
	logger *slog.Logger
}

// NewReceiver builds an IMAP receiver.
func NewReceiver(db *storage.DB, cfg IMAPConfig, logger *slog.Logger) *Receiver {
	if cfg.Folder == "" {
		cfg.Folder = "INBOX"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 60 * time.Second
	}
	return &Receiver{db: db, cfg: cfg, logger: logger}
}

// Run polls the mailbox until ctx is cancelled.
func (r *Receiver) Run(ctx context.Context) {
	if r.cfg.Host == "" {
		return
	}
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := r.poll(ctx); err != nil {
			r.logger.Warn("imap poll", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Receiver) dial() (*imapclient.Client, error) {
	addr := r.cfg.Host + ":" + strconv.Itoa(r.cfg.Port)
	switch r.cfg.TLS {
	case "none":
		return imapclient.DialInsecure(addr, nil)
	case "starttls":
		return imapclient.DialStartTLS(addr, nil)
	default:
		return imapclient.DialTLS(addr, nil)
	}
}

func (r *Receiver) poll(ctx context.Context) error {
	client, err := r.dial()
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Login(r.cfg.User, r.cfg.Pass).Wait(); err != nil {
		return err
	}
	defer client.Logout().Wait()

	if _, err := client.Select(r.cfg.Folder, nil).Wait(); err != nil {
		return err
	}

	searchData, err := client.UIDSearch(&imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}, nil).Wait()
	if err != nil {
		return err
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil
	}

	bodySection := &imap.FetchItemBodySection{}
	for _, uid := range uids {
		set := imap.UIDSetNum(uid)
		msgs, err := client.Fetch(set, &imap.FetchOptions{
			BodySection: []*imap.FetchItemBodySection{bodySection},
		}).Collect()
		if err != nil || len(msgs) == 0 {
			continue
		}
		raw := msgs[0].FindBodySection(bodySection)
		if raw == nil {
			continue
		}
		r.process(ctx, raw)

		// Mark handled: move to the processed folder if set, else flag Seen.
		if r.cfg.ProcessedFolder != "" {
			client.Move(set, r.cfg.ProcessedFolder).Wait()
		} else {
			client.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
		}
	}
	return nil
}

func (r *Receiver) process(ctx context.Context, raw []byte) {
	messageID := ""
	if msg, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		messageID = msg.Header.Get("Message-ID")
	}
	if messageID != "" {
		if seen, _ := r.db.IMIPProcessed(ctx, messageID); seen {
			return
		}
	}
	outcome, uid, err := ApplyReply(ctx, r.db, raw)
	if err != nil {
		r.logger.Warn("imip reply", "err", err, "outcome", outcome)
	}
	if messageID != "" {
		_ = r.db.MarkIMIPInbound(ctx, messageID, uid, outcome)
	}
	if outcome == OutcomeApplied {
		r.logger.Info("imip reply applied", "uid", uid)
	}
}
